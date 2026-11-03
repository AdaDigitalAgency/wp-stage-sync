package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/AdaDigitalAgency/wp-stage-sync/internal/config"
	"github.com/AdaDigitalAgency/wp-stage-sync/internal/db"
	"github.com/AdaDigitalAgency/wp-stage-sync/internal/discovery"
	"github.com/AdaDigitalAgency/wp-stage-sync/internal/export"
	"github.com/AdaDigitalAgency/wp-stage-sync/internal/guardrail"
	"github.com/AdaDigitalAgency/wp-stage-sync/internal/progress"
	"github.com/AdaDigitalAgency/wp-stage-sync/internal/selfupdate"
	"github.com/AdaDigitalAgency/wp-stage-sync/internal/sync"
	"github.com/AdaDigitalAgency/wp-stage-sync/internal/tui"
	"github.com/AdaDigitalAgency/wp-stage-sync/internal/wpconfig"
)

// Set via -ldflags at build time
var version = "dev"

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "WP Stage Sync - Sync a live WordPress site to its staging site, or promote\nthemes and plugins from staging to live.\n\n")
		fmt.Fprintf(os.Stderr, "Usage: wp-stage-sync [flags]\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		fmt.Fprintf(os.Stderr, "  -h, --help       Print this help message\n")
		fmt.Fprintf(os.Stderr, "  -v, --version    Print version and exit\n")
		fmt.Fprintf(os.Stderr, "  --update         Update to the latest version\n")
		fmt.Fprintf(os.Stderr, "  -u, --unattended Run in unattended mode using saved config\n")
		fmt.Fprintf(os.Stderr, "  -n, --dry-run    Plan the sync and print what would change, without writing\n")
		fmt.Fprintf(os.Stderr, "  --promote        Open the TUI in promote mode (stage → live)\n")
		fmt.Fprintf(os.Stderr, "  --restore        Open the TUI in restore mode\n")
		fmt.Fprintf(os.Stderr, "  -s, --site <id>  Target a specific site by identifier\n")
		fmt.Fprintf(os.Stderr, "  -l, --list       List all configured sites\n")
		fmt.Fprintf(os.Stderr, "  --delete         Delete a site config (requires --site)\n")
		fmt.Fprintf(os.Stderr, "  --reset          Remove all saved site configs\n\n")
		fmt.Fprintf(os.Stderr, "Examples:\n")
		fmt.Fprintf(os.Stderr, "  wp-stage-sync                        # Launch the TUI\n")
		fmt.Fprintf(os.Stderr, "  wp-stage-sync -l                     # List all configured sites\n")
		fmt.Fprintf(os.Stderr, "  wp-stage-sync -u -s my-site          # Sync 'my-site' unattended\n")
		fmt.Fprintf(os.Stderr, "  wp-stage-sync -n -s my-site          # Preview the sync for 'my-site' (no changes)\n")
		fmt.Fprintf(os.Stderr, "  wp-stage-sync --promote -s my-site   # Open the TUI in promote mode for 'my-site'\n")
		fmt.Fprintf(os.Stderr, "  wp-stage-sync --delete -s my-site    # Delete 'my-site' configuration\n")
		fmt.Fprintf(os.Stderr, "  wp-stage-sync --reset                # Wipe all configurations\n")
	}

	unattended := flag.Bool("u", false, "Run in unattended mode using saved config")
	flag.BoolVar(unattended, "unattended", false, "Run in unattended mode using saved config")
	dryRun := flag.Bool("dry-run", false, "Plan the sync without making any changes")
	flag.BoolVar(dryRun, "n", false, "Plan the sync without making any changes")
	showVersion := flag.Bool("version", false, "Print version and exit")
	flag.BoolVar(showVersion, "v", false, "Print version and exit")
	doUpdate := flag.Bool("update", false, "Update to the latest version")
	siteFlag := flag.String("site", "", "Site identifier (use --list to see configured sites)")
	flag.StringVar(siteFlag, "s", "", "Site identifier (use --list to see configured sites)")
	promoteFlag := flag.Bool("promote", false, "Enter promote mode (stage → live)")
	restoreFlag := flag.Bool("restore", false, "Enter restore mode")
	listFlag := flag.Bool("list", false, "List all configured sites")
	flag.BoolVar(listFlag, "l", false, "List all configured sites")
	deleteFlag := flag.Bool("delete", false, "Delete a site config (requires --site)")
	resetFlag := flag.Bool("reset", false, "Remove all saved site configs")
	flag.Parse()

	if *showVersion {
		fmt.Printf("wp-stage-sync %s\n", version)
		return
	}

	if *doUpdate {
		if err := selfupdate.Update(version); err != nil {
			fmt.Fprintf(os.Stderr, "Update failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Migrate legacy flat config files on every startup
	config.MigrateLegacyConfigs()

	if *listFlag {
		sites := config.ListSites()
		if len(sites) == 0 {
			fmt.Println("No sites configured. Run wp-stage-sync interactively to set one up.")
			return
		}
		for _, s := range sites {
			stageDomain := config.DomainFromPath(s.Config.StagePath)
			fmt.Printf("%s  →  %s\n", s.Domain, stageDomain)
		}
		return
	}

	if *deleteFlag {
		if *siteFlag == "" {
			fmt.Fprintf(os.Stderr, "The --delete flag requires --site to specify which site to remove.\n")
			os.Exit(1)
		}
		if !confirm(fmt.Sprintf("Delete all config and backups for %q?", *siteFlag)) {
			fmt.Println("Cancelled.")
			return
		}
		if err := config.DeleteSite(*siteFlag); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Deleted site config for %q.\n", *siteFlag)
		return
	}

	if *resetFlag {
		sites := config.ListSites()
		if len(sites) == 0 {
			fmt.Println("No sites configured. Nothing to reset.")
			return
		}
		fmt.Printf("This will delete %d site config(s) and all backups:\n", len(sites))
		for _, s := range sites {
			fmt.Printf("  %s\n", s.Domain)
		}
		if !confirm("Are you sure?") {
			fmt.Println("Cancelled.")
			return
		}
		if err := config.ResetAllSites(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("All site configs removed.")
		return
	}

	latestVersion := selfupdate.CheckVersion(version)

	// Validate flag combinations
	if *promoteFlag && *unattended {
		fmt.Fprintf(os.Stderr, "Promote mode is not available in unattended mode.\n")
		os.Exit(1)
	}
	if *restoreFlag && *unattended {
		fmt.Fprintf(os.Stderr, "Restore mode is not available in unattended mode.\n")
		os.Exit(1)
	}
	if *restoreFlag && *siteFlag != "" {
		fmt.Fprintf(os.Stderr, "Restore mode does not support --site. Use the interactive TUI.\n")
		os.Exit(1)
	}
	if *dryRun && (*promoteFlag || *restoreFlag) {
		fmt.Fprintf(os.Stderr, "Dry-run mode is only available for the default stage sync, not promote/restore.\n")
		os.Exit(1)
	}
	if *siteFlag != "" && !*unattended && !*promoteFlag && !*dryRun {
		fmt.Fprintf(os.Stderr, "The --site flag requires unattended mode (-u), dry-run mode (-n), or promote mode (--promote).\n")
		os.Exit(1)
	}

	if *dryRun {
		if err := runDryRun(*siteFlag); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if *unattended {
		if latestVersion != "" {
			fmt.Fprintf(os.Stderr, "\033[33mUpdate available: v%s → v%s. Run 'wp-stage-sync --update' to upgrade.\033[0m\n", version, latestVersion)
		}
		if err := runUnattended(*siteFlag); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Resolve config for promote/restore modes
	if *promoteFlag || *restoreFlag {
		cfg, err := resolveConfig(*siteFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		mode := tui.ModePromote
		if *restoreFlag {
			mode = tui.ModeRestore
		}
		if err := tui.RunWithMode(version, latestVersion, mode, cfg); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if err := tui.Run(version, latestVersion); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func resolveConfig(siteDomain string) (*config.Config, error) {
	sites := config.ListSites()

	switch {
	case siteDomain != "":
		cfg, err := config.Load(siteDomain)
		if err != nil {
			return nil, fmt.Errorf("no saved config for site %q: %w", siteDomain, err)
		}
		return cfg, nil
	case len(sites) == 1:
		return sites[0].Config, nil
	case len(sites) > 1:
		fmt.Fprintf(os.Stderr, "Multiple sites configured. Use --site to specify which one:\n")
		for _, s := range sites {
			fmt.Fprintf(os.Stderr, "  --site %s\n", s.Domain)
		}
		return nil, fmt.Errorf("--site flag required when multiple sites are configured")
	default:
		return nil, fmt.Errorf("no saved config found — run wp-stage-sync interactively first")
	}
}

func runUnattended(siteDomain string) error {
	log := progress.NewCLILogger()
	started := time.Now()

	// Resolve which site config to use
	sites := config.ListSites()
	var cfg *config.Config

	switch {
	case siteDomain != "":
		var err error
		cfg, err = config.Load(siteDomain)
		if err != nil {
			return fmt.Errorf("no saved config for site %q: %w", siteDomain, err)
		}
	case len(sites) == 1:
		cfg = sites[0].Config
	case len(sites) > 1:
		fmt.Fprintf(os.Stderr, "Multiple sites configured. Use --site to specify which one:\n")
		for _, s := range sites {
			fmt.Fprintf(os.Stderr, "  --site %s\n", s.Domain)
		}
		return fmt.Errorf("--site flag required when multiple sites are configured")
	default:
		return fmt.Errorf("no saved config found — run wp-stage-sync interactively first")
	}

	log.Detail(fmt.Sprintf("Site: %s", config.DomainFromPath(cfg.LivePath)))
	log.Detail(fmt.Sprintf("Stage: %s", cfg.StagePath))

	log.Step("Parsing wp-config files")
	liveWP, err := wpconfig.Parse(cfg.LivePath + "/wp-config.php")
	if err != nil {
		return fmt.Errorf("parsing live wp-config.php: %w", err)
	}
	stageWP, err := wpconfig.Parse(cfg.StagePath + "/wp-config.php")
	if err != nil {
		return fmt.Errorf("parsing stage wp-config.php: %w", err)
	}
	log.StepDone("Config parsed")

	// Safety: ensure live ≠ stage
	log.Step("Validating paths and databases")
	// One run per site at a time: a second sync, promote or restore would
	// write the same files and database.
	unlock, err := config.LockPaths(cfg.LivePath, cfg.StagePath)
	if err != nil {
		return err
	}
	defer unlock()
	if err := guardrail.ValidatePaths(cfg.LivePath, cfg.StagePath); err != nil {
		return err
	}
	if err := guardrail.ValidateDBs(liveWP, stageWP); err != nil {
		return err
	}
	log.StepDone("Validation passed")

	log.Step("Connecting to databases")
	liveDB, stageDB, err := db.Connect(liveWP, stageWP)
	if err != nil {
		return fmt.Errorf("database connection: %w", err)
	}
	defer liveDB.Close()
	defer stageDB.Close()
	log.StepDone("Connected")

	if err := guardrail.ValidateConnectedDBs(liveDB, stageDB); err != nil {
		return err
	}

	// Capture the real site hosts before import wipes the stage database.
	liveHost := sync.SiteHost(liveDB, liveWP.TablePrefix, discovery.ExtractDomain(cfg.LivePath))
	stageHost, err := sync.StageHost(stageDB, stageWP.TablePrefix, cfg.StagePath, liveHost)
	if err != nil {
		return err
	}

	// Before anything on stage changes: refuse if stage would share live's
	// object cache.
	log.Step("Checking object cache isolation")
	if err := sync.PreflightIntegrations(cfg.LivePath, cfg.StagePath, log); err != nil {
		return err
	}
	// Before anything on stage changes: every live setting that the stage
	// cleanup rewrites must be readable.
	if err := sync.CheckNeutralize(liveDB, liveWP.TablePrefix, cfg.LivePath, cfg.StagePath, liveHost, stageHost, cfg.StageObjectCacheOff); err != nil {
		return err
	}
	if err := sync.PreflightObjectCache(cfg.LivePath, cfg.StagePath, cfg.StageObjectCacheOff, log); err != nil {
		return err
	}
	log.StepDone("Object cache check passed")

	// Step 0: Export
	log.Step("Exporting from live database")
	exp, err := export.Run(liveDB, liveWP.TablePrefix, cfg, log)
	if err != nil {
		return fmt.Errorf("export: %w", err)
	}
	log.StepDone("Export complete")

	// Step 1: Import
	log.Step("Importing to staging database")
	if err := sync.Import(stageDB, exp, log); err != nil {
		return fmt.Errorf("import: %w", err)
	}
	log.StepDone("Import complete")

	// Before any WordPress code loads on stage: remove live's CDN credentials.
	if err := sync.Neutralize(stageDB, liveWP.TablePrefix, cfg.LivePath, cfg.StagePath, liveHost, stageHost, log); err != nil {
		return err
	}

	// Step 1b: Anonymize customer data (if enabled for this site)
	if cfg.Anonymize {
		log.Step("Anonymizing customer data")
		if err := sync.Anonymize(stageDB, liveWP.TablePrefix, log); err != nil {
			return fmt.Errorf("anonymize: %w", err)
		}
		log.StepDone("Anonymization complete")
	}

	// Step 2: File sync
	log.Step("Syncing files")
	excludes := sync.StageObjectCacheExcludes(sync.ResolveExcludes(cfg.RsyncExcludes), cfg.StageObjectCacheOff, cfg.LivePath)
	if err := sync.FileSync(cfg.LivePath, cfg.StagePath, excludes, log); err != nil {
		return fmt.Errorf("file sync: %w", err)
	}
	log.StepDone("File sync complete")

	// Before WordPress loads the new stage files: copied configs can hold
	// live's absolute paths.
	if err := sync.FixLivePaths(cfg.LivePath, cfg.StagePath, liveHost, stageHost, log); err != nil {
		return err
	}
	if cfg.StageObjectCacheOff {
		if err := sync.DisableStageObjectCache(cfg.StagePath, stageDB, liveWP.TablePrefix, log); err != nil {
			return err
		}
	}

	// Step 3: Post-processing
	log.Step("Post-processing")
	if err := sync.PostProcess(cfg.LivePath, cfg.StagePath, liveHost, stageHost, log); err != nil {
		return fmt.Errorf("post-processing: %w", err)
	}
	log.StepDone("Post-processing complete")

	duration := time.Since(started).Truncate(time.Second)
	log.StepDone(fmt.Sprintf("Sync completed at %s and it took %s", time.Now().Format("2006-01-02 15:04:05"), duration))
	return nil
}

func runDryRun(siteDomain string) error {
	log := progress.NewCLILogger()

	cfg, err := resolveConfig(siteDomain)
	if err != nil {
		return err
	}

	liveDomain := discovery.ExtractDomain(cfg.LivePath)
	stageDomain := discovery.ExtractDomain(cfg.StagePath)

	fmt.Println("DRY RUN — no database, file, or WP-CLI changes will be made.")
	fmt.Println()
	fmt.Printf("Live:  %s (%s)\n", liveDomain, cfg.LivePath)
	fmt.Printf("Stage: %s (%s)\n", stageDomain, cfg.StagePath)

	log.Step("Parsing wp-config files")
	liveWP, err := wpconfig.Parse(cfg.LivePath + "/wp-config.php")
	if err != nil {
		return fmt.Errorf("parsing live wp-config.php: %w", err)
	}
	stageWP, err := wpconfig.Parse(cfg.StagePath + "/wp-config.php")
	if err != nil {
		return fmt.Errorf("parsing stage wp-config.php: %w", err)
	}
	log.StepDone("Config parsed")

	log.Step("Validating paths and databases")
	if err := guardrail.ValidatePaths(cfg.LivePath, cfg.StagePath); err != nil {
		return err
	}
	if err := guardrail.ValidateDBs(liveWP, stageWP); err != nil {
		return err
	}
	log.StepDone("Validation passed")

	log.Step("Connecting to databases")
	liveDB, stageDB, err := db.Connect(liveWP, stageWP)
	if err != nil {
		return fmt.Errorf("database connection: %w", err)
	}
	defer liveDB.Close()
	defer stageDB.Close()
	log.StepDone("Connected")

	if err := guardrail.ValidateConnectedDBs(liveDB, stageDB); err != nil {
		return err
	}

	// Plan the export (read-only — counts rows, dumps nothing).
	log.Step("Planning export from live database")
	plan, err := export.BuildPlan(liveDB, liveWP.TablePrefix, cfg, log)
	if err != nil {
		return fmt.Errorf("export plan: %w", err)
	}
	log.StepDone("Export planned")

	// Determine which staging tables would be dropped before import.
	stageTables, err := sync.ListStageTables(stageDB)
	if err != nil {
		return fmt.Errorf("listing staging tables: %w", err)
	}

	// Plan the file sync via rsync --dry-run.
	log.Step("Planning file sync (rsync --dry-run)")
	excludes := sync.StageObjectCacheExcludes(sync.ResolveExcludes(cfg.RsyncExcludes), cfg.StageObjectCacheOff, cfg.LivePath)
	rsyncPlan, rsyncErr := sync.FileSyncPlan(cfg.LivePath, cfg.StagePath, excludes)
	if rsyncErr != nil {
		log.Detail(fmt.Sprintf("rsync preview unavailable: %v", rsyncErr))
	}
	log.StepDone("File sync planned")

	liveHost := sync.SiteHost(liveDB, liveWP.TablePrefix, liveDomain)
	stageHost := sync.SiteHost(stageDB, stageWP.TablePrefix, stageDomain)
	ppPlan := sync.PostProcessPlan(cfg.LivePath, cfg.StagePath, liveHost, stageHost, cfg.StageObjectCacheOff)

	printExportPlan(plan)
	printImportPlan(stageTables, plan)
	printAnonymizePlan(cfg.Anonymize)
	printRsyncPlan(rsyncPlan, excludes, rsyncErr)
	printPostProcessPlan(ppPlan)
	fmt.Println()
	fmt.Println("── Live integrations switched off on stage ───────────────────")
	for _, name := range sync.IntegrationNames() {
		fmt.Printf("  %s (settings cleared after the import)\n", name)
	}
	if err := sync.CheckNeutralize(liveDB, liveWP.TablePrefix, cfg.LivePath, cfg.StagePath, liveHost, stageHost, cfg.StageObjectCacheOff); err != nil {
		fmt.Printf("  [check] FAILED: %v\n", err)
	} else {
		fmt.Println("  [check] all live settings are readable; a real sync can clear them on stage")
	}

	fmt.Println()
	fmt.Println("No changes were made. Re-run with -u (without -n) to execute the sync.")
	return nil
}

func printExportPlan(p *export.Plan) {
	fmt.Println()
	fmt.Println("── Database export (from live) ───────────────────────────────")
	if p.WooCommerce {
		fmt.Println("WooCommerce:    detected — order/user filtering applied")
		fmt.Printf("Target orders:  %d (%s %d)\n", p.TargetOrders, p.OrderPreference, p.OrderCount)
		fmt.Printf("Safe users:     %d\n", p.SafeUsers)
	} else {
		fmt.Println("WooCommerce:    not detected — full export (no order/user filtering)")
	}
	fmt.Printf("Tables in live: %d\n", p.TotalTables)

	var filtered, full, schemaOnly, ignored []export.TablePlan
	for _, t := range p.Tables {
		switch {
		case t.Mode == config.TableModeIgnore:
			ignored = append(ignored, t)
		case t.Mode == config.TableModeStructureOnly:
			schemaOnly = append(schemaOnly, t)
		case t.Filter != "":
			filtered = append(filtered, t)
		default:
			full = append(full, t)
		}
	}

	if len(filtered) > 0 {
		fmt.Printf("\nFiltered tables (%d) — partial data by rule:\n", len(filtered))
		for _, t := range filtered {
			fmt.Printf("  %-40s %8d rows  [%s]\n", t.Name, t.Rows, t.Filter)
		}
	}
	if len(full) > 0 {
		fmt.Printf("\nFull-data tables (%d):\n", len(full))
		for _, t := range full {
			fmt.Printf("  %-40s %8d rows\n", t.Name, t.Rows)
		}
	}
	if len(schemaOnly) > 0 {
		fmt.Printf("\nSchema-only tables (%d) — structure, no rows:\n", len(schemaOnly))
		for _, t := range schemaOnly {
			fmt.Printf("  %s\n", t.Name)
		}
	}
	if len(ignored) > 0 {
		fmt.Printf("\nIgnored tables (%d) — not exported:\n", len(ignored))
		for _, t := range ignored {
			fmt.Printf("  %s\n", t.Name)
		}
	}
	fmt.Printf("\nTotal rows that would be exported: %d\n", p.ExportedRows())
}

func printImportPlan(stageTables []string, p *export.Plan) {
	fmt.Println()
	fmt.Println("── Database import (into stage) ──────────────────────────────")
	fmt.Printf("Would drop %d existing staging table(s), then recreate %d table(s) from the export.\n",
		len(stageTables), len(p.Tables))
}

func printAnonymizePlan(anonymize bool) {
	fmt.Println()
	fmt.Println("── Anonymization ─────────────────────────────────────────────")
	if anonymize {
		fmt.Println("Enabled — users, usermeta, order addresses, order records (email/IP/notes),")
		fmt.Println("and order notes would be masked after import.")
	} else {
		fmt.Println("Disabled — customer data would be imported as-is.")
	}
}

func printRsyncPlan(plan *sync.RsyncPlan, excludes []string, rsyncErr error) {
	fmt.Println()
	fmt.Println("── File sync (rsync) ─────────────────────────────────────────")
	if plan != nil {
		fmt.Printf("Source: %s\n", plan.Source)
		fmt.Printf("Dest:   %s\n", plan.Dest)
	}
	fmt.Printf("Excludes: %s\n", strings.Join(excludes, ", "))
	if plan != nil {
		fmt.Printf("Command: %s\n", plan.Command)
	}
	if rsyncErr != nil {
		fmt.Printf("(rsync preview unavailable: %v)\n", rsyncErr)
		return
	}
	if plan != nil && plan.Output != "" {
		fmt.Println("\nrsync --dry-run stats:")
		fmt.Println(plan.Output)
	}
}

func printPostProcessPlan(plan *sync.PostProcessReport) {
	fmt.Println()
	fmt.Println("── Post-processing (WP-CLI) ──────────────────────────────────")
	if !plan.WPAvailable {
		fmt.Println("Note: wp-cli is not installed locally; it would be downloaded at run time.")
	}
	fmt.Printf("Object cache: %s\n", plan.ObjectCache)
	switch {
	case plan.CacheOff:
		fmt.Println("  [off]   stage object cache is switched off: no drop-in on stage")
	case plan.ObjectCache != "no persistent object cache":
		fmt.Println("  [check] stage and live must not share cache keys (probe key, at sync time)")
		if plan.SharedCache != "" {
			fmt.Printf("  WARNING: stage and live share %s. A purge in the stage admin can clear live's cache.\n", plan.SharedCache)
			fmt.Println("           Give stage its own database, or switch the stage object cache off in the TUI.")
		}
	}
	for _, c := range plan.Commands {
		if c.Skip {
			fmt.Printf("  [skip] %s (%s)\n", c.Label, c.SkipNote)
			continue
		}
		fmt.Printf("  [run]  %s\n", c.Label)
		if len(c.Remove) > 0 || len(c.RemoveFiles) > 0 {
			fmt.Printf("         clear %s\n", strings.Join(append(append([]string{}, c.Remove...), c.RemoveFiles...), ", "))
			continue
		}
		if strings.HasPrefix(c.Label, "Cache flush (") {
			fmt.Println("         wp eval <" + strings.TrimSuffix(strings.SplitN(c.Label, "(", 2)[1], ")") + ">")
			continue
		}
		fmt.Printf("         %s\n", strings.Join(c.Args, " "))
	}
}

func confirm(prompt string) bool {
	fmt.Printf("%s [y/N] ", prompt)
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
		return answer == "y" || answer == "yes"
	}
	return false
}
