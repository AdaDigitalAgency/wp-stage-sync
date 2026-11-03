package whatsnew

// Entries holds the release notes shown in the TUI, written for the person
// using it. Keep each line to MaxLineWidth characters and at most three lines
// per version. Most patch releases need no entry.
//
// Add lines under "unreleased" in the same commit as the work. The release tool
// renames that key to the version.
var Entries = map[string][]string{
	"unreleased": {
		"Sync no longer purges live Cloudflare or object cache",
		"Stage CDN keys and caches of 16 plugins cleared",
		"Option to switch off a shared stage object cache",
	},
	"0.7.0": {
		"Excludes now apply to top-level folders only",
		"Nested folders like plugins/foo/cache now sync",
		"Uncheck every folder to exclude nothing",
	},
}
