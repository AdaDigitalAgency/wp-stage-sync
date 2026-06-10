package tui

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/AdaDigitalAgency/wp-stage-sync/internal/config"
	"github.com/AdaDigitalAgency/wp-stage-sync/internal/db"
	"github.com/AdaDigitalAgency/wp-stage-sync/internal/discovery"
	"github.com/AdaDigitalAgency/wp-stage-sync/internal/export"
	"github.com/AdaDigitalAgency/wp-stage-sync/internal/guardrail"
	"github.com/AdaDigitalAgency/wp-stage-sync/internal/progress"
	"github.com/AdaDigitalAgency/wp-stage-sync/internal/promote"
	"github.com/AdaDigitalAgency/wp-stage-sync/internal/sync"
	"github.com/AdaDigitalAgency/wp-stage-sync/internal/wpconfig"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type step int

const (
	stepStartup step = iota
	stepPaths
	stepCredentials
	stepSyncParams
	stepTableSelect
	stepExcludes
	stepConfirm
	stepRunning
	stepDone
	// Promote flow
	stepPromoteSelect
	stepPromoteConfirm
	stepPromoteRunning
	stepPromoteDone
	// Restore flow
	stepRestoreList
	stepRestoreConfirm
	stepRestoreRunning
	stepRestoreDone
	// Delete flow
	stepDeleteSelect
	stepDeleteConfirm
	// Settings flow
	stepSettings
)

// RunMode controls which flow the TUI starts in.
type RunMode int

const (
	ModeDefault RunMode = iota
	ModePromote
	ModeRestore
)

type pathMode int

const (
	pathModePairs  pathMode = iota // selecting from auto-detected pairs
	pathModeManual                 // typing paths manually
)

type model struct {
	step           step
	cfg            *config.Config
	savedSites     []config.SavedSite
	discovered     []string
	sitePairs      []discovery.SitePair
	pathMode       pathMode
	curDir         string
	dirItems       []string
	dirCursor      int
	liveWP         *wpconfig.WPConfig
	stageWP        *wpconfig.WPConfig
	allTables      []string
	excludeItems   []string        // wp-content subfolders available for exclusion
	excludeChecked map[string]bool // true = excluded from rsync
	cursor         int
	input          string
	err            error
	status         string
	width          int
	height         int

	// Progress tracking for stepRunning
	completedSteps  []string // steps that finished (shown with ✓)
	currentStep     string   // step currently in progress
	currentDetail   string   // detail message under current step
	progressCurrent int
	progressTotal   int
	spinnerFrame    int
	currentVersion  string
	updateAvailable string // latest version, empty if up to date

	// Promote flow
	promoteAllItems    []promote.SelectedItem // all available items (flat list)
	promoteAssetLabels []string               // display labels for each item
	promoteChecked     map[int]bool           // indices selected by user

	// Restore flow
	restoreBackups []promote.BackupMeta // available backups for current domain

	// Delete flow
	deleteChecked map[int]bool // indices of savedSites selected for deletion

	// Help screen
	showHelp bool

	// Settings flow
	settings config.Settings
}

type syncDoneMsg struct{ err error }
type stepMsg struct{ name string }
type detailMsg struct{ msg string }
type progressMsg struct{ current, total int }
type stepDoneMsg struct{ msg string }
type spinTickMsg struct{}

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12")).MarginBottom(1)
	promptStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))
	errorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	successStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	updateStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).MarginTop(1)
	sectionStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
)

var activeProgram *tea.Program
var syncStartedAt time.Time
var syncCompletedAt time.Time

func Run(currentVersion, latestVersion string) error {
	m := initialModel()
	m.currentVersion = currentVersion
	m.updateAvailable = latestVersion
	p := tea.NewProgram(m, tea.WithAltScreen())
	activeProgram = p
	_, err := p.Run()
	if err == nil && !syncCompletedAt.IsZero() {
		duration := syncCompletedAt.Sub(syncStartedAt).Truncate(time.Second)
		fmt.Printf("Sync completed at %s and it took %s\n", syncCompletedAt.Format("2006-01-02 15:04:05"), duration)
	}
	return err
}

// RunWithMode launches the TUI directly into promote or restore mode.
func RunWithMode(currentVersion, latestVersion string, mode RunMode, cfg *config.Config) error {
	m := initialModel()
	m.currentVersion = currentVersion
	m.updateAvailable = latestVersion
	m.cfg = cfg

	switch mode {
	case ModePromote:
		m = m.enterPromoteMode()
	case ModeRestore:
		m = m.enterRestoreMode()
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	activeProgram = p
	_, err := p.Run()
	return err
}

func initialModel() model {
	sites := config.ListSites()
	discovered := discovery.Scan()
	pairs := discovery.PairSites(discovered)

	var curDir string
	if home, err := os.UserHomeDir(); err == nil {
		curDir = home
	} else {
		curDir = "/"
	}

	m := model{
		discovered: discovered,
		sitePairs:  pairs,
		savedSites: sites,
		curDir:     curDir,
		cfg: &config.Config{
			OrderCount:      100,
			OrderPreference: "last",
			TableModes:      make(map[string]config.TableMode),
		},
	}

	if len(sites) > 0 {
		m.step = stepStartup
	} else {
		m.step = stepPaths
	}

	m = m.readSubdirs()
	return m
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case syncDoneMsg:
		switch m.step {
		case stepPromoteRunning:
			m.step = stepPromoteDone
		case stepRestoreRunning:
			m.step = stepRestoreDone
		default:
			m.step = stepDone
		}
		m.err = msg.err
		syncCompletedAt = time.Now()
		return m, nil

	case stepMsg:
		m.currentStep = msg.name
		m.currentDetail = ""
		m.progressCurrent = 0
		m.progressTotal = 0
		return m, nil

	case detailMsg:
		m.currentDetail = msg.msg
		return m, nil

	case progressMsg:
		m.progressCurrent = msg.current
		m.progressTotal = msg.total
		return m, nil

	case stepDoneMsg:
		m.completedSteps = append(m.completedSteps, msg.msg)
		m.currentStep = ""
		m.currentDetail = ""
		m.progressCurrent = 0
		m.progressTotal = 0
		return m, nil

	case spinTickMsg:
		if m.step == stepRunning || m.step == stepPromoteRunning || m.step == stepRestoreRunning {
			m.spinnerFrame++
			return m, tickSpinner()
		}
		return m, nil

	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC || msg.String() == "q" {
			return m, tea.Quit
		}

		if msg.String() == "h" && m.step != stepRunning && m.step != stepPromoteRunning && m.step != stepRestoreRunning {
			m.showHelp = !m.showHelp
			return m, nil
		}

		if m.showHelp {
			if msg.Type == tea.KeyEsc || msg.String() == "q" {
				m.showHelp = false
			}
			return m, nil
		}

		if msg.Type == tea.KeyEsc && m.step != stepRunning && m.step != stepDone &&
			m.step != stepPromoteRunning && m.step != stepPromoteDone &&
			m.step != stepRestoreRunning && m.step != stepRestoreDone &&
			m.step != stepDeleteConfirm {
			m.err = nil
			m = m.goBack()
			return m, nil
		}

		if msg.String() == "s" && (m.step == stepStartup || (m.step == stepPaths && m.pathMode == pathModePairs)) {
			m.settings = config.LoadSettings()
			m.step = stepSettings
			m.cursor = 0
			return m, nil
		}

		m.err = nil

		switch m.step {
		case stepStartup:
			return m.updateStartup(msg)
		case stepPaths:
			return m.updatePaths(msg)
		case stepCredentials:
			return m.updateCredentials(msg)
		case stepSyncParams:
			return m.updateSyncParams(msg)
		case stepTableSelect:
			return m.updateTableSelect(msg)
		case stepExcludes:
			return m.updateExcludes(msg)
		case stepConfirm:
			return m.updateConfirm(msg)
		case stepDone:
			if msg.Type == tea.KeyEnter {
				return m, tea.Quit
			}
		// Promote flow
		case stepPromoteSelect:
			return m.updatePromoteSelect(msg)
		case stepPromoteConfirm:
			return m.updatePromoteConfirm(msg)
		case stepPromoteDone:
			if msg.Type == tea.KeyEnter {
				return m, tea.Quit
			}
		// Restore flow
		case stepRestoreList:
			return m.updateRestoreList(msg)
		case stepRestoreConfirm:
			return m.updateRestoreConfirm(msg)
		case stepRestoreDone:
			if msg.Type == tea.KeyEnter {
				return m, tea.Quit
			}
		// Delete flow
		case stepDeleteSelect:
			return m.updateDeleteSelect(msg)
		case stepDeleteConfirm:
			return m.updateDeleteConfirm(msg)
		// Settings flow
		case stepSettings:
			return m.updateSettings(msg)
		}
	}
	return m, nil
}

func (m model) View() string {
	var b strings.Builder

	if (m.step == stepStartup || m.step == stepPaths) && m.width >= 60 && m.height >= 24 {
		// Generated using https://patorjk.com/software/taag/#p=display&f=Rebel&t=WP+STAGE%0A+++++++SYNC&x=none&v=4&h=4&w=80&we=false
		banner := titleStyle.Render(`
   █████   ███   █████ ███████████      █████████  ███████████   █████████     █████████  ██████████
  ░░███   ░███  ░░███ ░░███░░░░░███    ███░░░░░███░█░░░███░░░█  ███░░░░░███   ███░░░░░███░░███░░░░░█
   ░███   ░███   ░███  ░███    ░███   ░███    ░░░ ░   ░███  ░  ░███    ░███  ███     ░░░  ░███  █ ░ 
   ░███   ░███   ░███  ░██████████    ░░█████████     ░███     ░███████████ ░███          ░██████   
   ░░███  █████  ███   ░███░░░░░░      ░░░░░░░░███    ░███     ░███░░░░░███ ░███    █████ ░███░░█   
    ░░░█████░█████░    ░███            ███    ░███    ░███     ░███    ░███ ░░███  ░░███  ░███ ░   █
      ░░███ ░░███      █████          ░░█████████     █████    █████   █████ ░░█████████  ██████████
       ░░░   ░░░      ░░░░░            ░░░░░░░░░     ░░░░░    ░░░░░   ░░░░░   ░░░░░░░░░  ░░░░░░░░░░ 
                                                                                                    
                                                                                                    
                                                                                                    
                         █████████  █████ █████ ██████   █████   █████████                          
                        ███░░░░░███░░███ ░░███ ░░██████ ░░███   ███░░░░░███                         
                       ░███    ░░░  ░░███ ███   ░███░███ ░███  ███     ░░░                          
                       ░░█████████   ░░█████    ░███░░███░███ ░███                                  
                        ░░░░░░░░███   ░░███     ░███ ░░██████ ░███                                  
                        ███    ░███    ░███     ░███  ░░█████ ░░███     ███                         
                       ░░█████████     █████    █████  ░░█████ ░░█████████                          
                        ░░░░░░░░░     ░░░░░    ░░░░░    ░░░░░   ░░░░░░░░░`) + "\n\n"
		b.WriteString(banner)
	} else {
		b.WriteString(titleStyle.Render("🔄 WP Stage Sync") + "\n\n")
	}

	if m.showHelp {
		b.WriteString(promptStyle.Render("Help & Keyboard Shortcuts:") + "\n\n")
		b.WriteString(sectionStyle.Render("Global") + "\n")
		b.WriteString("  " + selectedStyle.Render("h") + "      Toggle this help screen\n")
		b.WriteString("  " + selectedStyle.Render("q") + "      Quit the application\n")
		b.WriteString("  " + selectedStyle.Render("Esc") + "    Go back to the previous screen\n\n")

		b.WriteString(sectionStyle.Render("Startup Screen") + "\n")
		b.WriteString("  " + selectedStyle.Render("p") + "      Enter promote mode for the selected site\n")
		b.WriteString("  " + selectedStyle.Render("r") + "      Enter restore mode for the selected site\n")
		b.WriteString("  " + selectedStyle.Render("d") + "      Delete site configurations\n\n")

		b.WriteString(sectionStyle.Render("Lists & Selections") + "\n")
		b.WriteString("  " + selectedStyle.Render("↑/↓") + "    Navigate lists\n")
		b.WriteString("  " + selectedStyle.Render("Space") + "  Toggle selection (checkboxes)\n")
		b.WriteString("  " + selectedStyle.Render("a") + "      Select all items\n")
		b.WriteString("  " + selectedStyle.Render("Enter") + "  Confirm selection\n\n")

		b.WriteString("\n" + dimStyle.Render("Press Esc or h to close help"))
	} else {
		switch m.step {
		case stepStartup:
			b.WriteString(promptStyle.Render("Saved configurations:") + "\n\n")
			for i, s := range m.savedSites {
				stageDomain := config.DomainFromPath(s.Config.StagePath)
				label := fmt.Sprintf("%s  →  %s", s.Domain, stageDomain)
				if i == m.cursor {
					b.WriteString(selectedStyle.Render("▸ "+label) + "\n")
				} else {
					b.WriteString("  " + label + "\n")
				}
			}
			// "Configure new site" option
			newIdx := len(m.savedSites)
			if m.cursor == newIdx {
				b.WriteString(selectedStyle.Render("▸ Configure new site...") + "\n")
			} else {
				b.WriteString("  " + dimStyle.Render("Configure new site...") + "\n")
			}

		case stepPaths:
			if m.pathMode == pathModePairs && len(m.sitePairs) > 0 {
				b.WriteString(promptStyle.Render("Select a site to sync:") + "\n\n")
				for i, pair := range m.sitePairs {
					marker := "  "
					if i == m.cursor {
						marker = selectedStyle.Render("▸ ")
					}
					if pair.StagePath != "" {
						label := fmt.Sprintf("%s  →  %s", pair.Domain, "stage."+pair.Domain)
						if i == m.cursor {
							b.WriteString(marker + selectedStyle.Render(label) + "\n")
						} else {
							b.WriteString(marker + label + "\n")
						}
					} else {
						label := pair.Domain + dimStyle.Render("  (no staging found)")
						if i == m.cursor {
							b.WriteString(marker + selectedStyle.Render(pair.Domain) + dimStyle.Render("  (no staging found)") + "\n")
						} else {
							b.WriteString(marker + label + "\n")
						}
					}
				}
				// Custom paths option
				marker := "  "
				if m.cursor == len(m.sitePairs) {
					marker = selectedStyle.Render("▸ ")
					b.WriteString(marker + selectedStyle.Render("Custom paths...") + "\n")
				} else {
					b.WriteString(marker + dimStyle.Render("Custom paths...") + "\n")
				}
			} else {
				// Manual directory selection mode
				if m.cursor == 0 {
					b.WriteString(promptStyle.Render("Select Live Webroot:") + "\n\n")
				} else {
					b.WriteString(promptStyle.Render("Select Stage Webroot:") + "\n\n")
				}
				b.WriteString(dimStyle.Render("Current: "+m.curDir) + "\n\n")

				visible := m.pageSize()
				if visible > len(m.dirItems) {
					visible = len(m.dirItems)
				}
				start := 0
				if m.dirCursor >= visible {
					start = m.dirCursor - visible + 1
				}
				end := start + visible
				if end > len(m.dirItems) {
					end = len(m.dirItems)
				}

				for i := start; i < end; i++ {
					prefix := "  "
					style := lipgloss.NewStyle()
					if i == m.dirCursor {
						prefix = "▸ "
						style = selectedStyle
					}
					b.WriteString(style.Render(prefix+m.dirItems[i]) + "\n")
				}
				b.WriteString("\n" + dimStyle.Render("↑/↓ navigate | Enter open folder | Backspace go back to parent folder | Space select current folder | Esc go back to previous screen"))
			}

		case stepCredentials:
			b.WriteString(promptStyle.Render("Extracted credentials:") + "\n\n")
			b.WriteString(fmt.Sprintf("  Live DB: %s @ %s (prefix: %s)\n", m.liveWP.DBName, m.liveWP.DBHost, m.liveWP.TablePrefix))
			b.WriteString(fmt.Sprintf("  Stage DB: %s @ %s (prefix: %s)\n", m.stageWP.DBName, m.stageWP.DBHost, m.stageWP.TablePrefix))
			b.WriteString("\n" + dimStyle.Render("Press Enter to continue"))

		case stepSyncParams:
			b.WriteString(promptStyle.Render("Sync parameters:") + "\n\n")

			// 1. Order Count
			if m.cursor == 0 {
				b.WriteString(selectedStyle.Render("▸ Order Count: ") + m.input + "█\n")
			} else {
				b.WriteString(fmt.Sprintf("  Order Count: %d\n", m.cfg.OrderCount))
			}

			// 2. Order Preference
			pref := "Last N"
			if m.cfg.OrderPreference == "first" {
				pref = "First N"
			}
			if m.cursor == 1 {
				b.WriteString(selectedStyle.Render("▸ Order Preference: ") + selectedStyle.Render(pref) + " (Press ↑/↓ to toggle)\n")
			} else {
				b.WriteString("  Order Preference: " + pref + "\n")
			}

			// 3. Anonymize Customers
			anonText := "No"
			if m.cfg.Anonymize {
				anonText = "Yes"
			}
			if m.cursor == 2 {
				b.WriteString(selectedStyle.Render("▸ Anonymize Customers: ") + selectedStyle.Render(anonText) + " (Press ↑/↓ to toggle)\n")
			} else {
				b.WriteString("  Anonymize Customers: " + anonText + "\n")
			}

			b.WriteString("\n" + dimStyle.Render("Tab to switch fields, Enter to confirm"))

		case stepTableSelect:
			b.WriteString(promptStyle.Render("Table sync modes:") + "\n")
			b.WriteString(dimStyle.Render("↑/↓ navigate, PgUp/PgDn jump, Space to cycle mode, Enter to confirm") + "\n\n")

			// Show visible window of tables
			windowSize := m.height - 10
			if windowSize < 5 {
				windowSize = 5
			}
			start := 0
			if m.cursor >= windowSize {
				start = m.cursor - windowSize + 1
			}
			end := start + windowSize
			if end > len(m.allTables) {
				end = len(m.allTables)
			}

			for i := start; i < end; i++ {
				t := m.allTables[i]
				mode := getDisplayMode(m.cfg, t, m.liveWP.TablePrefix)
				prefix := "  "
				style := lipgloss.NewStyle()
				if i == m.cursor {
					prefix = "▸ "
					style = selectedStyle
				}
				b.WriteString(style.Render(fmt.Sprintf("%s%-50s [%s]", prefix, t, mode)) + "\n")
			}

		case stepExcludes:
			b.WriteString(promptStyle.Render("Rsync excludes (wp-content/):") + "\n")
			b.WriteString(dimStyle.Render("Space to toggle, Enter to confirm, Esc to go back") + "\n\n")

			// Viewport
			visible := m.pageSize()
			if visible > len(m.excludeItems) {
				visible = len(m.excludeItems)
			}
			start := 0
			if m.cursor >= visible {
				start = m.cursor - visible + 1
			}
			end := start + visible
			if end > len(m.excludeItems) {
				end = len(m.excludeItems)
			}

			for i := start; i < end; i++ {
				item := m.excludeItems[i]
				prefix := "  "
				style := lipgloss.NewStyle()
				if i == m.cursor {
					prefix = "▸ "
					style = selectedStyle
				}
				check := "[ ]"
				if m.excludeChecked[item] {
					check = "[x]"
				}
				label := fmt.Sprintf("%s%s %s", prefix, check, item)
				b.WriteString(style.Render(label) + "\n")
			}

			excluded := 0
			for _, v := range m.excludeChecked {
				if v {
					excluded++
				}
			}
			b.WriteString("\n" + dimStyle.Render(fmt.Sprintf("%d/%d excluded", excluded, len(m.excludeItems))))

		case stepConfirm:
			b.WriteString(promptStyle.Render("Ready to sync?") + "\n\n")
			b.WriteString(fmt.Sprintf("  Live:  %s\n", m.cfg.LivePath))
			b.WriteString(fmt.Sprintf("  Stage: %s\n", m.cfg.StagePath))

			// Check if WooCommerce tables exist
			hasWoo := false
			for _, t := range m.allTables {
				if t == m.liveWP.TablePrefix+"wc_orders" || t == m.liveWP.TablePrefix+"woocommerce_order_items" {
					hasWoo = true
					break
				}
			}
			if hasWoo {
				anonText := "no"
				if m.cfg.Anonymize {
					anonText = "yes"
				}
				b.WriteString(fmt.Sprintf("  Orders: %d (%s, anonymize: %s)\n", m.cfg.OrderCount, m.cfg.OrderPreference, anonText))
			} else {
				b.WriteString("  WooCommerce: not installed (full WordPress sync)\n")
			}
			b.WriteString("\n" + dimStyle.Render("Press Enter to start, Esc to go back"))

		case stepRunning:
			b.WriteString(promptStyle.Render("Syncing...") + "\n\n")
			b.WriteString(m.renderProgress())

		case stepDone:
			b.WriteString(m.renderDone("Sync"))

		// --- Promote flow ---
		case stepPromoteSelect:
			b.WriteString(promptStyle.Render("Promote to live — select items:") + "\n")
			b.WriteString(dimStyle.Render("Space to toggle, Enter to confirm, Esc to go back") + "\n\n")

			visible := m.pageSize()
			if visible > len(m.promoteAssetLabels) {
				visible = len(m.promoteAssetLabels)
			}
			start := 0
			if m.cursor >= visible {
				start = m.cursor - visible + 1
			}
			end := start + visible
			if end > len(m.promoteAssetLabels) {
				end = len(m.promoteAssetLabels)
			}

			for i := start; i < end; i++ {
				label := m.promoteAssetLabels[i]
				prefix := "  "
				style := lipgloss.NewStyle()
				if i == m.cursor {
					prefix = "▸ "
					style = selectedStyle
				}
				// Section headers (start with ──)
				if strings.HasPrefix(label, "──") {
					b.WriteString(sectionStyle.Render(prefix+label) + "\n")
					continue
				}
				check := "[ ]"
				if m.promoteChecked[i] {
					check = "[x]"
				}
				b.WriteString(style.Render(fmt.Sprintf("%s%s %s", prefix, check, label)) + "\n")
			}

			selected := 0
			for _, v := range m.promoteChecked {
				if v {
					selected++
				}
			}
			b.WriteString("\n" + dimStyle.Render(fmt.Sprintf("%d selected", selected)))

		case stepPromoteConfirm:
			b.WriteString(promptStyle.Render("Ready to promote to live?") + "\n\n")
			for _, item := range m.selectedPromoteItems() {
				b.WriteString(fmt.Sprintf("  %s: %s\n", item.Type, item.Name))
				b.WriteString("    " + dimStyle.Render("stage: "+item.StagePath) + "\n")
				b.WriteString("    " + dimStyle.Render("live:  "+item.LivePath) + "\n\n")
			}
			b.WriteString(dimStyle.Render("Press Enter to start, Esc to go back"))

		case stepPromoteRunning:
			b.WriteString(promptStyle.Render("Promoting to live...") + "\n\n")
			b.WriteString(m.renderProgress())

		case stepPromoteDone:
			b.WriteString(m.renderDone("Promote"))

		// --- Restore flow ---
		case stepRestoreList:
			b.WriteString(promptStyle.Render("Select a backup to restore:") + "\n\n")
			if len(m.restoreBackups) == 0 {
				b.WriteString(dimStyle.Render("  No backups found for this site.") + "\n")
			} else {
				visible := m.pageSize()
				if visible > len(m.restoreBackups) {
					visible = len(m.restoreBackups)
				}
				start := 0
				if m.cursor >= visible {
					start = m.cursor - visible + 1
				}
				end := start + visible
				if end > len(m.restoreBackups) {
					end = len(m.restoreBackups)
				}

				for i := start; i < end; i++ {
					bk := m.restoreBackups[i]
					prefix := "  "
					style := lipgloss.NewStyle()
					if i == m.cursor {
						prefix = "▸ "
						style = selectedStyle
					}
					// Format timestamp nicely
					ts := strings.Replace(bk.Timestamp, "_", " ", 1)
					var itemNames []string
					for _, item := range bk.Items {
						itemNames = append(itemNames, string(item.Type)+":"+item.Name)
					}
					summary := strings.Join(itemNames, ", ")
					b.WriteString(style.Render(fmt.Sprintf("%s%s — %s", prefix, ts, summary)) + "\n")
				}
			}
			b.WriteString("\n" + dimStyle.Render("Enter to select, Esc to go back"))

		case stepRestoreConfirm:
			bk := m.restoreBackups[m.cursor]
			ts := strings.Replace(bk.Timestamp, "_", " ", 1)
			b.WriteString(promptStyle.Render(fmt.Sprintf("Restore backup from %s?", ts)) + "\n\n")
			for _, item := range bk.Items {
				b.WriteString(fmt.Sprintf("  %s: %s\n", item.Type, item.Name))
				b.WriteString("    " + dimStyle.Render("→ "+item.LivePath) + "\n\n")
			}
			b.WriteString(dimStyle.Render("Press Enter to restore, Esc to go back"))

		case stepRestoreRunning:
			b.WriteString(promptStyle.Render("Restoring...") + "\n\n")
			b.WriteString(m.renderProgress())

		case stepRestoreDone:
			b.WriteString(m.renderDone("Restore"))

		case stepDeleteSelect:
			b.WriteString(promptStyle.Render("Select configs to delete:") + "\n\n")
			for i, s := range m.savedSites {
				stageDomain := config.DomainFromPath(s.Config.StagePath)
				label := fmt.Sprintf("%s  →  %s", s.Domain, stageDomain)
				check := "[ ]"
				if m.deleteChecked[i] {
					check = "[x]"
				}
				if i == m.cursor {
					b.WriteString(selectedStyle.Render(fmt.Sprintf("▸ %s %s", check, label)) + "\n")
				} else {
					b.WriteString(fmt.Sprintf("  %s %s", check, label) + "\n")
				}
			}
			b.WriteString("\n" + dimStyle.Render("Space to toggle, a select all, Enter to confirm, Esc to go back"))

		case stepDeleteConfirm:
			var selected []string
			for i, s := range m.savedSites {
				if m.deleteChecked[i] {
					selected = append(selected, s.Domain)
				}
			}
			b.WriteString(errorStyle.Render(fmt.Sprintf("Delete %d site config(s) and their backups?", len(selected))) + "\n\n")
			for _, name := range selected {
				b.WriteString(fmt.Sprintf("  • %s\n", name))
			}
			b.WriteString("\n" + dimStyle.Render("Press Enter to delete, Esc to go back"))

		case stepSettings:
			b.WriteString(promptStyle.Render("Settings:") + "\n\n")

			// Backup Retention
			prefix := "  "
			style := lipgloss.NewStyle()
			if m.cursor == 0 {
				prefix = "▸ "
				style = selectedStyle
			}
			b.WriteString(style.Render(fmt.Sprintf("%sBackup retention (max per site): %d", prefix, m.settings.BackupRetention)) + "\n")
			if m.cursor == 0 {
				b.WriteString(dimStyle.Render("    Use ←/→ to decrease/increase") + "\n")
			} else {
				b.WriteString("\n")
			}

			// Auto cache flush
			prefix = "  "
			style = lipgloss.NewStyle()
			if m.cursor == 1 {
				prefix = "▸ "
				style = selectedStyle
			}
			status := "ON"
			if !m.settings.AutoCacheFlush {
				status = "OFF"
			}
			b.WriteString(style.Render(fmt.Sprintf("%sAuto cache flush after sync/promote: %s", prefix, status)) + "\n")
			if m.cursor == 1 {
				b.WriteString(dimStyle.Render("    Use Space/Enter to toggle") + "\n")
			} else {
				b.WriteString("\n")
			}

			b.WriteString("\n" + dimStyle.Render("Press Esc or q to save and exit"))
		}

		if m.err != nil && m.step != stepDone && m.step != stepPromoteDone && m.step != stepRestoreDone {
			b.WriteString("\n" + errorStyle.Render("Error: "+m.err.Error()))
		}

		if m.updateAvailable != "" {
			b.WriteString("\n" + updateStyle.Render(fmt.Sprintf("Update available: v%s → run 'wp-stage-sync --update'", m.updateAvailable)))
		}
	}

	// Global footer
	leftText := m.footerLeft()
	rightText := fmt.Sprintf("wp-stage-sync v%s", m.currentVersion)
	if m.updateAvailable != "" {
		rightText = fmt.Sprintf("wp-stage-sync v%s (update available: v%s)", m.currentVersion, m.updateAvailable)
	}

	padding := m.width - lipgloss.Width(leftText) - lipgloss.Width(rightText)
	if padding < 2 {
		padding = 2
	}

	footerLine := "\n\n" + dimStyle.Render(leftText+strings.Repeat(" ", padding)+rightText)
	b.WriteString(footerLine)

	return b.String()
}

// --- View helpers ---

func (m model) renderProgress() string {
	var b strings.Builder
	for _, s := range m.completedSteps {
		b.WriteString(successStyle.Render("  ✓ "+s) + "\n")
	}
	if m.currentStep != "" {
		spinChars := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		spin := spinChars[m.spinnerFrame%len(spinChars)]
		b.WriteString(selectedStyle.Render("  "+spin+" "+m.currentStep) + "\n")

		if m.currentDetail != "" {
			b.WriteString(dimStyle.Render("    "+m.currentDetail) + "\n")
		}

		if m.progressTotal > 0 {
			barWidth := 30
			filled := barWidth * m.progressCurrent / m.progressTotal
			if filled > barWidth {
				filled = barWidth
			}
			bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
			pct := 100 * m.progressCurrent / m.progressTotal
			b.WriteString(dimStyle.Render(fmt.Sprintf("    [%s] %d/%d (%d%%)", bar, m.progressCurrent, m.progressTotal, pct)) + "\n")
		}
	}
	return b.String()
}

func (m model) renderDone(label string) string {
	var b strings.Builder
	for _, s := range m.completedSteps {
		b.WriteString(successStyle.Render("  ✓ "+s) + "\n")
	}
	b.WriteString("\n")
	if m.err != nil {
		b.WriteString(errorStyle.Render(fmt.Sprintf("✗ %s failed: %s", label, m.err.Error())) + "\n")
	} else {
		duration := syncCompletedAt.Sub(syncStartedAt).Truncate(time.Second)
		b.WriteString(successStyle.Render(fmt.Sprintf("✓ %s complete! (took %s)", label, duration)) + "\n")
	}
	b.WriteString("\n" + dimStyle.Render("Press Enter or q to exit"))
	return b.String()
}

func (m model) footerLeft() string {
	if m.showHelp {
		return " esc back"
	}
	base := " q quit | esc back | h help"
	if m.step == stepStartup || (m.step == stepPaths && m.pathMode == pathModePairs) {
		base += " | s settings"
	}
	if m.step == stepStartup && len(m.savedSites) > 0 {
		extra := " | d delete"
		if m.cursor < len(m.savedSites) {
			domain := m.savedSites[m.cursor].Domain
			extra += " | p promote"
			if config.HasBackups(domain) {
				extra += " | r restore"
			}
		}
		return base + extra
	}
	return base
}

// --- Step handlers ---

func (m model) updateStartup(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	maxIdx := len(m.savedSites) // includes "Configure new site"
	switch msg.Type {
	case tea.KeyUp:
		if m.cursor > 0 {
			m.cursor--
		}
	case tea.KeyDown:
		if m.cursor < maxIdx {
			m.cursor++
		}
	case tea.KeyEnter:
		if m.cursor == len(m.savedSites) {
			// Configure new site
			m.step = stepPaths
			m.cursor = 0
			m.input = ""
			return m, nil
		}
		// Use saved site config
		m.cfg = m.savedSites[m.cursor].Config
		m.step = stepCredentials
		m.cursor = 0
		var err error
		m.liveWP, err = wpconfig.Parse(m.cfg.LivePath + "/wp-config.php")
		if err != nil {
			m.err = err
			return m, nil
		}
		m.stageWP, err = wpconfig.Parse(m.cfg.StagePath + "/wp-config.php")
		if err != nil {
			m.err = err
			return m, nil
		}
	default:
		switch msg.String() {
		case "p":
			if m.cursor < len(m.savedSites) {
				m.cfg = m.savedSites[m.cursor].Config
				m = m.enterPromoteMode()
			}
		case "r":
			if m.cursor < len(m.savedSites) {
				domain := m.savedSites[m.cursor].Domain
				if config.HasBackups(domain) {
					m.cfg = m.savedSites[m.cursor].Config
					m = m.enterRestoreMode()
				}
			}
		case "d":
			if len(m.savedSites) > 0 {
				m.step = stepDeleteSelect
				m.cursor = 0
				m.deleteChecked = make(map[int]bool)
			}
		}
	}
	return m, nil
}

func (m model) enterPromoteMode() model {
	themes, plugins, muPlugins := promote.ListAssets(m.cfg.StagePath)

	var allItems []promote.SelectedItem
	var labels []string
	wpContent := filepath.Join(m.cfg.StagePath, "wp-content")
	liveWPContent := filepath.Join(m.cfg.LivePath, "wp-content")

	if len(themes) > 0 {
		labels = append(labels, "── Themes ──")
		allItems = append(allItems, promote.SelectedItem{}) // placeholder for section header
		for _, name := range themes {
			allItems = append(allItems, promote.SelectedItem{
				Type:      promote.AssetTheme,
				Name:      name,
				StagePath: filepath.Join(wpContent, "themes", name),
				LivePath:  filepath.Join(liveWPContent, "themes", name),
			})
			labels = append(labels, name)
		}
	}

	if len(plugins) > 0 {
		labels = append(labels, "── Plugins ──")
		allItems = append(allItems, promote.SelectedItem{}) // placeholder
		for _, name := range plugins {
			allItems = append(allItems, promote.SelectedItem{
				Type:      promote.AssetPlugin,
				Name:      name,
				StagePath: filepath.Join(wpContent, "plugins", name),
				LivePath:  filepath.Join(liveWPContent, "plugins", name),
			})
			labels = append(labels, name)
		}
	}

	if len(muPlugins) > 0 {
		labels = append(labels, "── MU-Plugins ──")
		allItems = append(allItems, promote.SelectedItem{}) // placeholder
		for _, name := range muPlugins {
			allItems = append(allItems, promote.SelectedItem{
				Type:      promote.AssetMuPlugin,
				Name:      name,
				StagePath: filepath.Join(wpContent, "mu-plugins", name),
				LivePath:  filepath.Join(liveWPContent, "mu-plugins", name),
			})
			labels = append(labels, name)
		}
	}

	m.promoteAllItems = allItems
	m.promoteAssetLabels = labels
	m.promoteChecked = make(map[int]bool)
	m.step = stepPromoteSelect
	m.cursor = 0
	// Skip to first non-header item
	if len(labels) > 0 && strings.HasPrefix(labels[0], "──") {
		m.cursor = 1
	}
	return m
}

func (m model) enterRestoreMode() model {
	domain := config.DomainFromPath(m.cfg.LivePath)
	backups, _ := promote.ListBackups(domain)
	m.restoreBackups = backups
	m.step = stepRestoreList
	m.cursor = 0
	return m
}

func (m model) selectedPromoteItems() []promote.SelectedItem {
	var items []promote.SelectedItem
	for idx, checked := range m.promoteChecked {
		if checked && idx < len(m.promoteAllItems) {
			item := m.promoteAllItems[idx]
			if item.Name != "" { // skip section headers
				items = append(items, item)
			}
		}
	}
	return items
}

func (m model) updatePromoteSelect(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	last := len(m.promoteAssetLabels) - 1
	switch msg.Type {
	case tea.KeyUp:
		if m.cursor > 0 {
			m.cursor--
			// Skip section headers
			if strings.HasPrefix(m.promoteAssetLabels[m.cursor], "──") && m.cursor > 0 {
				m.cursor--
			}
		}
	case tea.KeyDown:
		if m.cursor < last {
			m.cursor++
			// Skip section headers
			if m.cursor <= last && strings.HasPrefix(m.promoteAssetLabels[m.cursor], "──") {
				if m.cursor < last {
					m.cursor++
				}
			}
		}
	case tea.KeySpace:
		// Don't toggle section headers
		if !strings.HasPrefix(m.promoteAssetLabels[m.cursor], "──") {
			m.promoteChecked[m.cursor] = !m.promoteChecked[m.cursor]
		}
	case tea.KeyEnter:
		items := m.selectedPromoteItems()
		if len(items) == 0 {
			m.err = fmt.Errorf("select at least one item to promote")
			return m, nil
		}
		m.step = stepPromoteConfirm
	}
	return m, nil
}

func (m model) updatePromoteConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyEnter {
		items := m.selectedPromoteItems()
		m.step = stepPromoteRunning
		m.completedSteps = nil
		m.currentStep = ""
		m.currentDetail = ""
		m.progressCurrent = 0
		m.progressTotal = 0
		m.spinnerFrame = 0

		syncStartedAt = time.Now()
		return m, tea.Batch(
			func() tea.Msg {
				err := runPromote(m.cfg, items, activeProgram)
				return syncDoneMsg{err: err}
			},
			tickSpinner(),
		)
	}
	return m, nil
}

func (m model) updateRestoreList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if len(m.restoreBackups) == 0 {
		return m, nil
	}
	last := len(m.restoreBackups) - 1
	switch msg.Type {
	case tea.KeyUp:
		if m.cursor > 0 {
			m.cursor--
		}
	case tea.KeyDown:
		if m.cursor < last {
			m.cursor++
		}
	case tea.KeyEnter:
		m.step = stepRestoreConfirm
	}
	return m, nil
}

func (m model) updateRestoreConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyEnter {
		bk := m.restoreBackups[m.cursor]
		m.step = stepRestoreRunning
		m.completedSteps = nil
		m.currentStep = ""
		m.currentDetail = ""
		m.progressCurrent = 0
		m.progressTotal = 0
		m.spinnerFrame = 0

		syncStartedAt = time.Now()
		return m, tea.Batch(
			func() tea.Msg {
				err := runRestore(m.cfg, &bk, activeProgram)
				return syncDoneMsg{err: err}
			},
			tickSpinner(),
		)
	}
	return m, nil
}

func (m model) updateDeleteSelect(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyUp:
		if m.cursor > 0 {
			m.cursor--
		}
	case tea.KeyDown:
		if m.cursor < len(m.savedSites)-1 {
			m.cursor++
		}
	case tea.KeyEnter:
		// Check at least one is selected
		anySelected := false
		for _, v := range m.deleteChecked {
			if v {
				anySelected = true
				break
			}
		}
		if !anySelected {
			m.err = fmt.Errorf("no sites selected — use Space to toggle")
			return m, nil
		}
		m.step = stepDeleteConfirm
		return m, nil
	default:
		switch msg.String() {
		case " ":
			m.deleteChecked[m.cursor] = !m.deleteChecked[m.cursor]
		case "a":
			// Select all and go to confirm
			for i := range m.savedSites {
				m.deleteChecked[i] = true
			}
			m.step = stepDeleteConfirm
		}
	}
	return m, nil
}

func (m model) updateDeleteConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		for i := len(m.savedSites) - 1; i >= 0; i-- {
			if m.deleteChecked[i] {
				_ = config.DeleteSite(m.savedSites[i].Domain)
			}
		}
		// Refresh saved sites
		m.savedSites = config.ListSites()
		m.deleteChecked = nil
		m.cursor = 0
		if len(m.savedSites) > 0 {
			m.step = stepStartup
		} else {
			m.step = stepPaths
		}
	case tea.KeyEsc:
		m.step = stepDeleteSelect
		m.cursor = 0
	}
	return m, nil
}

func (m model) updateSettings(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyUp:
		if m.cursor > 0 {
			m.cursor--
		}
	case tea.KeyDown:
		if m.cursor < 1 {
			m.cursor++
		}
	case tea.KeyLeft:
		if m.cursor == 0 && m.settings.BackupRetention > 1 {
			m.settings.BackupRetention--
		}
	case tea.KeyRight:
		if m.cursor == 0 {
			m.settings.BackupRetention++
		}
	case tea.KeySpace, tea.KeyEnter:
		if m.cursor == 1 {
			m.settings.AutoCacheFlush = !m.settings.AutoCacheFlush
		}
	case tea.KeyEsc:
		// Save settings and go back
		_ = config.SaveSettings(m.settings)
		if len(m.savedSites) > 0 {
			m.step = stepStartup
		} else {
			m.step = stepPaths
		}
		m.cursor = 0
	}

	if msg.Type == tea.KeyRunes && msg.String() == "q" {
		_ = config.SaveSettings(m.settings)
		return m, tea.Quit
	}

	if msg.Type == tea.KeyRunes && msg.String() == "s" {
		_ = config.SaveSettings(m.settings)
		if len(m.savedSites) > 0 {
			m.step = stepStartup
		} else {
			m.step = stepPaths
		}
		m.cursor = 0
	}

	return m, nil
}

// --- Promote/Restore runners ---

func runPromote(cfg *config.Config, items []promote.SelectedItem, p *tea.Program) error {
	log := &tuiLogger{p: p}

	// Validate
	log.Step("Validating paths")
	if err := guardrail.ValidatePaths(cfg.LivePath, cfg.StagePath); err != nil {
		return err
	}
	if err := guardrail.ValidateWPContentPaths(cfg.LivePath, cfg.StagePath); err != nil {
		return err
	}
	if err := promote.ValidatePromote(items); err != nil {
		return err
	}
	log.StepDone("Validation passed")

	// Backup
	domain := config.DomainFromPath(cfg.LivePath)
	log.Step("Creating backup of live files")
	archivePath, err := promote.CreateBackup(domain, items, log)
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	log.StepDone("Backup created")

	// Enforce retention
	settings := config.LoadSettings()
	if err := promote.EnforceRetention(domain, settings.BackupRetention); err != nil {
		log.Detail(fmt.Sprintf("Retention cleanup warning: %v", err))
	}

	// Execute
	if err := promote.Execute(items, cfg.LivePath, archivePath, log); err != nil {
		return err
	}
	log.StepDone("Promote complete")

	return nil
}

func runRestore(cfg *config.Config, bk *promote.BackupMeta, p *tea.Program) error {
	log := &tuiLogger{p: p}

	domain := config.DomainFromPath(cfg.LivePath)
	archivePath, err := promote.BackupArchivePath(domain, bk.Timestamp)
	if err != nil {
		return err
	}

	log.Step("Restoring from backup")
	if err := promote.RestoreFromBackup(archivePath, bk, cfg.LivePath, log); err != nil {
		return fmt.Errorf("restore: %w", err)
	}
	log.StepDone("Restore complete")

	return nil
}

// --- Existing step handlers (unchanged) ---

func (m model) updatePaths(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.pathMode == pathModePairs && len(m.sitePairs) > 0 {
		return m.updatePathsPairs(msg)
	}
	return m.updatePathsManual(msg)
}

func (m model) updatePathsPairs(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	maxIdx := len(m.sitePairs) // includes "Custom paths" option
	switch msg.Type {
	case tea.KeyUp:
		if m.cursor > 0 {
			m.cursor--
		}
	case tea.KeyDown:
		if m.cursor < maxIdx {
			m.cursor++
		}
	case tea.KeyEnter:
		if m.cursor == len(m.sitePairs) {
			// Custom paths
			m.pathMode = pathModeManual
			m.cursor = 0
			m.input = ""
			return m, nil
		}
		pair := m.sitePairs[m.cursor]
		if pair.StagePath == "" {
			m.err = fmt.Errorf("no staging site found for %s — use Custom paths", pair.Domain)
			return m, nil
		}
		// Try to load saved config for this domain
		if saved, err := config.Load(pair.Domain); err == nil {
			m.cfg = saved
		}
		m.cfg.LivePath = pair.LivePath
		m.cfg.StagePath = pair.StagePath
		var err error
		m.liveWP, err = wpconfig.Parse(m.cfg.LivePath + "/wp-config.php")
		if err != nil {
			m.err = fmt.Errorf("live wp-config: %w", err)
			return m, nil
		}
		m.stageWP, err = wpconfig.Parse(m.cfg.StagePath + "/wp-config.php")
		if err != nil {
			m.err = fmt.Errorf("stage wp-config: %w", err)
			return m, nil
		}
		m.step = stepCredentials
		m.cursor = 0
	}
	return m, nil
}

func (m model) updatePathsManual(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	last := len(m.dirItems) - 1
	switch msg.Type {
	case tea.KeyEsc:
		if len(m.sitePairs) > 0 {
			m.pathMode = pathModePairs
			m.cursor = 0
			m.input = ""
		}
	case tea.KeyUp:
		if m.dirCursor > 0 {
			m.dirCursor--
		}
	case tea.KeyDown:
		if m.dirCursor < last {
			m.dirCursor++
		}
	case tea.KeyEnter:
		if len(m.dirItems) == 0 {
			break
		}
		item := m.dirItems[m.dirCursor]
		if item == ".." {
			m.curDir = filepath.Dir(m.curDir)
		} else {
			m.curDir = filepath.Join(m.curDir, item)
		}
		m = m.readSubdirs()
	case tea.KeyBackspace:
		m.curDir = filepath.Dir(m.curDir)
		m = m.readSubdirs()
	default:
		if msg.String() == " " {
			if len(m.dirItems) == 0 {
				break
			}
			path := m.curDir
			item := m.dirItems[m.dirCursor]
			if item != ".." {
				path = filepath.Join(m.curDir, item)
			}

			if m.cursor == 0 {
				m.cfg.LivePath = path
				m.cursor = 1
				m.curDir = filepath.Dir(path)
				m = m.readSubdirs()
			} else {
				m.cfg.StagePath = path
				var err error
				m.liveWP, err = wpconfig.Parse(m.cfg.LivePath + "/wp-config.php")
				if err != nil {
					m.err = fmt.Errorf("live wp-config: %w", err)
					m.cursor = 0
					return m, nil
				}
				m.stageWP, err = wpconfig.Parse(m.cfg.StagePath + "/wp-config.php")
				if err != nil {
					m.err = fmt.Errorf("stage wp-config: %w", err)
					m.cursor = 0
					return m, nil
				}
				m.step = stepCredentials
				m.cursor = 0
			}
		}
	}
	return m, nil
}

func (m model) readSubdirs() model {
	entries, err := os.ReadDir(m.curDir)
	if err != nil {
		m.err = err
		m.dirItems = []string{".."}
		m.dirCursor = 0
		return m
	}
	m.dirItems = []string{".."}
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			m.dirItems = append(m.dirItems, e.Name())
		}
	}
	m.dirCursor = 0
	return m
}

func (m model) updateCredentials(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyEnter {
		liveDB, _, err := db.Connect(m.liveWP, m.stageWP)
		if err != nil {
			m.err = fmt.Errorf("connecting to DB: %w", err)
			return m, nil
		}
		tables, err := queryTables(liveDB)
		liveDB.Close()
		if err != nil {
			m.err = fmt.Errorf("listing tables: %w", err)
			return m, nil
		}
		m.allTables = tables

		// Check both plugin directory and DB tables
		wooPluginPath := filepath.Join(m.cfg.LivePath, "wp-content", "plugins", "woocommerce")
		info, errStat := os.Stat(wooPluginPath)
		hasWoo := errStat == nil && info.IsDir()

		if !hasWoo {
			for _, t := range tables {
				if t == m.liveWP.TablePrefix+"wc_orders" || t == m.liveWP.TablePrefix+"woocommerce_order_items" {
					hasWoo = true
					break
				}
			}
		}

		if hasWoo {
			m.step = stepSyncParams
			m.cursor = 0
			m.input = fmt.Sprintf("%d", m.cfg.OrderCount)
		} else {
			m.step = stepTableSelect
			m.cursor = 0
			applyDefaultModes(m.cfg, tables, m.liveWP.TablePrefix)
		}
	}
	return m, nil
}

func (m model) updateSyncParams(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyTab:
		if m.cursor == 0 {
			n, err := strconv.Atoi(m.input)
			if err != nil || n <= 0 {
				m.err = fmt.Errorf("invalid order count")
				return m, nil
			}
			m.cfg.OrderCount = n
			m.cursor = 1
		} else if m.cursor == 1 {
			m.cursor = 2
		} else {
			m.cursor = 0
			m.input = fmt.Sprintf("%d", m.cfg.OrderCount)
		}
	case tea.KeyEnter:
		if m.cursor == 0 {
			n, err := strconv.Atoi(m.input)
			if err != nil || n <= 0 {
				m.err = fmt.Errorf("invalid order count")
				return m, nil
			}
			m.cfg.OrderCount = n
			m.cursor = 1
		} else if m.cursor == 1 {
			m.cursor = 2
		} else {
			m.step = stepTableSelect
			m.cursor = 0
			applyDefaultModes(m.cfg, m.allTables, m.liveWP.TablePrefix)
		}
	case tea.KeyUp, tea.KeyDown:
		if m.cursor == 1 {
			if m.cfg.OrderPreference == "last" {
				m.cfg.OrderPreference = "first"
			} else {
				m.cfg.OrderPreference = "last"
			}
		} else if m.cursor == 2 {
			m.cfg.Anonymize = !m.cfg.Anonymize
		}
	case tea.KeyBackspace:
		if m.cursor == 0 && len(m.input) > 0 {
			m.input = m.input[:len(m.input)-1]
		}
	default:
		if m.cursor == 0 && msg.Type == tea.KeyRunes {
			m.input += string(msg.Runes)
		}
	}
	return m, nil
}

func (m model) pageSize() int {
	p := m.height - 10
	if p < 5 {
		p = 5
	}
	return p
}

func (m model) updateTableSelect(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	pageSize := m.pageSize()
	last := len(m.allTables) - 1

	switch msg.Type {
	case tea.KeyUp:
		if m.cursor > 0 {
			m.cursor--
		}
	case tea.KeyDown:
		if m.cursor < last {
			m.cursor++
		}
	case tea.KeyPgUp:
		m.cursor -= pageSize
		if m.cursor < 0 {
			m.cursor = 0
		}
	case tea.KeyPgDown:
		m.cursor += pageSize
		if m.cursor > last {
			m.cursor = last
		}
	case tea.KeyHome:
		m.cursor = 0
	case tea.KeyEnd:
		m.cursor = last
	case tea.KeySpace:
		t := m.allTables[m.cursor]
		short := strings.TrimPrefix(t, m.liveWP.TablePrefix)
		builtin := export.DefaultTableMode(short)
		if builtin == config.TableModeCustomRule || builtin == config.TableModeStructureOnly {
			break // built-in rule, not user-configurable
		}
		m.cfg.TableModes[t] = cycleMode(m.cfg.TableModes[t])
	case tea.KeyEnter:
		m.excludeItems, m.excludeChecked = buildExcludeList(m.cfg)
		m.step = stepExcludes
		m.cursor = 0
	}
	return m, nil
}

func buildExcludeList(cfg *config.Config) ([]string, map[string]bool) {
	// Scan wp-content for subdirectories
	wpContentPath := filepath.Join(cfg.LivePath, "wp-content")
	entries, _ := os.ReadDir(wpContentPath)

	var items []string
	for _, e := range entries {
		if e.IsDir() {
			items = append(items, e.Name())
		}
	}

	// Build checked map: start with defaults, then overlay saved config
	checked := make(map[string]bool)
	if len(cfg.RsyncExcludes) > 0 {
		// Use saved excludes
		for _, ex := range cfg.RsyncExcludes {
			checked[ex] = true
		}
	} else {
		// Use defaults — only check items that actually exist
		for _, def := range sync.DefaultExcludes {
			for _, item := range items {
				if item == def {
					checked[def] = true
					break
				}
			}
		}
	}

	return items, checked
}

func (m model) updateExcludes(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	last := len(m.excludeItems) - 1
	switch msg.Type {
	case tea.KeyUp:
		if m.cursor > 0 {
			m.cursor--
		}
	case tea.KeyDown:
		if m.cursor < last {
			m.cursor++
		}
	case tea.KeyPgUp:
		m.cursor -= m.pageSize()
		if m.cursor < 0 {
			m.cursor = 0
		}
	case tea.KeyPgDown:
		m.cursor += m.pageSize()
		if m.cursor > last {
			m.cursor = last
		}
	case tea.KeyHome:
		m.cursor = 0
	case tea.KeyEnd:
		m.cursor = last
	case tea.KeySpace:
		item := m.excludeItems[m.cursor]
		m.excludeChecked[item] = !m.excludeChecked[item]
	case tea.KeyEnter:
		// Save checked items to config
		var excludes []string
		for _, item := range m.excludeItems {
			if m.excludeChecked[item] {
				excludes = append(excludes, item)
			}
		}
		m.cfg.RsyncExcludes = excludes
		m.step = stepConfirm
		m.cursor = 0
	}
	return m, nil
}

func (m model) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		m.step = stepRunning
		m.status = "Starting..."
		m.completedSteps = nil
		m.currentStep = ""
		m.currentDetail = ""
		m.progressCurrent = 0
		m.progressTotal = 0
		m.spinnerFrame = 0
		config.Save(m.cfg)

		syncStartedAt = time.Now()
		return m, tea.Batch(
			func() tea.Msg {
				err := runSync(m.cfg, m.liveWP, m.stageWP, activeProgram)
				return syncDoneMsg{err: err}
			},
			tickSpinner(),
		)
	}
	return m, nil
}

// --- Helpers ---

func runSync(cfg *config.Config, liveWP, stageWP *wpconfig.WPConfig, p *tea.Program) error {
	log := &tuiLogger{p: p}

	// Safety: ensure live ≠ stage
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

	// Capture the real site hosts before import wipes the stage database.
	liveHost := sync.SiteHost(liveDB, liveWP.TablePrefix, discovery.ExtractDomain(cfg.LivePath))
	stageHost := sync.SiteHost(stageDB, stageWP.TablePrefix, discovery.ExtractDomain(cfg.StagePath))

	log.Step("Exporting from live database")
	exp, err := export.Run(liveDB, liveWP.TablePrefix, cfg, log)
	if err != nil {
		return fmt.Errorf("export: %w", err)
	}
	log.StepDone("Export complete")

	log.Step("Importing to staging database")
	if err := sync.Import(stageDB, exp, log); err != nil {
		return fmt.Errorf("import: %w", err)
	}
	log.StepDone("Import complete")

	if cfg.Anonymize {
		log.Step("Anonymizing customer data")
		if err := sync.Anonymize(stageDB, liveWP.TablePrefix, log); err != nil {
			return fmt.Errorf("anonymize: %w", err)
		}
		log.StepDone("Anonymization complete")
	}

	log.Step("Syncing files")
	if err := sync.FileSync(cfg.LivePath, cfg.StagePath, cfg.RsyncExcludes, log); err != nil {
		return fmt.Errorf("file sync: %w", err)
	}
	log.StepDone("File sync complete")

	log.Step("Post-processing")
	if err := sync.PostProcess(cfg.StagePath, liveHost, stageHost, log); err != nil {
		return fmt.Errorf("post-processing: %w", err)
	}
	log.StepDone("Post-processing complete")

	return nil
}

func queryTables(liveDB *sql.DB) ([]string, error) {
	rows, err := liveDB.Query("SHOW TABLES")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		tables = append(tables, t)
	}
	return tables, rows.Err()
}

func applyDefaultModes(cfg *config.Config, tables []string, prefix string) {
	for _, t := range tables {
		if _, exists := cfg.TableModes[t]; exists {
			continue
		}
		short := strings.TrimPrefix(t, prefix)
		cfg.TableModes[t] = export.DefaultTableMode(short)
	}
}

func cycleMode(current config.TableMode) config.TableMode {
	switch current {
	case config.TableModeStructureAndData:
		return config.TableModeStructureOnly
	case config.TableModeStructureOnly:
		return config.TableModeIgnore
	case config.TableModeIgnore:
		return config.TableModeCustomRule
	default:
		return config.TableModeStructureAndData
	}
}

func getDisplayMode(cfg *config.Config, table, prefix string) string {
	short := strings.TrimPrefix(table, prefix)
	builtin := export.DefaultTableMode(short)
	// Built-in rules always take precedence
	mode := builtin
	if builtin == config.TableModeStructureAndData {
		if saved, ok := cfg.TableModes[table]; ok {
			mode = saved
		}
	}
	switch mode {
	case config.TableModeStructureAndData:
		return "Structure & Data"
	case config.TableModeStructureOnly:
		return "Structure Only"
	case config.TableModeIgnore:
		return "Ignore"
	case config.TableModeCustomRule:
		return "Custom Rule"
	default:
		return string(mode)
	}
}

// --- TUI Logger ---

type tuiLogger struct {
	p *tea.Program
}

func (l *tuiLogger) Step(name string) {
	l.p.Send(stepMsg{name: name})
}

func (l *tuiLogger) Detail(msg string) {
	l.p.Send(detailMsg{msg: msg})
}

func (l *tuiLogger) Progress(current, total int) {
	l.p.Send(progressMsg{current: current, total: total})
}

func (l *tuiLogger) StepDone(msg string) {
	l.p.Send(stepDoneMsg{msg: msg})
}

var _ progress.Logger = (*tuiLogger)(nil)

func tickSpinner() tea.Cmd {
	return tea.Tick(80*time.Millisecond, func(_ time.Time) tea.Msg {
		return spinTickMsg{}
	})
}

func (m model) goBack() model {
	switch m.step {
	case stepPaths:
		if m.pathMode == pathModeManual {
			if m.cursor == 1 {
				m.cursor = 0
				if m.cfg.LivePath != "" {
					m.curDir = filepath.Dir(m.cfg.LivePath)
					m = m.readSubdirs()
				}
			} else if len(m.sitePairs) > 0 {
				m.pathMode = pathModePairs
				m.cursor = 0
			} else if len(m.savedSites) > 0 {
				m.step = stepStartup
				m.cursor = 0
			}
		} else if len(m.savedSites) > 0 {
			m.step = stepStartup
			m.cursor = 0
		}
	case stepCredentials:
		m.step = stepPaths
		m.cursor = 0
		m = m.readSubdirs()
	case stepSyncParams:
		m.step = stepCredentials
		m.cursor = 0
	case stepTableSelect:
		// Check if WooCommerce was detected
		hasWoo := false
		for _, t := range m.allTables {
			if t == m.liveWP.TablePrefix+"wc_orders" || t == m.liveWP.TablePrefix+"woocommerce_order_items" {
				hasWoo = true
				break
			}
		}
		if hasWoo {
			m.step = stepSyncParams
			m.cursor = 0
			m.input = fmt.Sprintf("%d", m.cfg.OrderCount)
		} else {
			m.step = stepCredentials
			m.cursor = 0
		}
	case stepExcludes:
		m.step = stepTableSelect
		m.cursor = 0
	case stepConfirm:
		m.step = stepExcludes
		m.cursor = 0
	// Promote flow
	case stepPromoteSelect:
		if len(m.savedSites) > 0 {
			m.step = stepStartup
			m.cursor = 0
		}
	case stepPromoteConfirm:
		m.step = stepPromoteSelect
		m.cursor = 0
	// Restore flow
	case stepRestoreList:
		if len(m.savedSites) > 0 {
			m.step = stepStartup
			m.cursor = 0
		}
	case stepRestoreConfirm:
		m.step = stepRestoreList
		m.cursor = 0
	// Delete flow
	case stepDeleteSelect:
		if len(m.savedSites) > 0 {
			m.step = stepStartup
			m.cursor = 0
		}
	case stepDeleteConfirm:
		m.step = stepDeleteSelect
		m.cursor = 0
	// Settings flow
	case stepSettings:
		_ = config.SaveSettings(m.settings)
		if len(m.savedSites) > 0 {
			m.step = stepStartup
			m.cursor = 0
		} else {
			m.step = stepPaths
			m.cursor = 0
		}
	}
	return m
}
