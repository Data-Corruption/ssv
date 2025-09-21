package config

import "time"

/*
Once used in a released version, this struct cannot be changed.
If you need to change it, create a new struct and use it in the migration.

type ExampleV2 struct {
	x string
}

type Example struct {
	x int
}
*/

// Version is the current version of the schema
const Version = "v1.0.0"

// key -> default value
type schema map[string]valueInterface

// SchemaRecord is a version -> schema map of all released and the current schema. For defaults and migration purposes.
// After making changes to the schema, before the next release you must add a new version entry to this variable
// and migration funcs for it in `migration.go`. The newest version is assumed to be the current version.
var SchemaRecord = map[string]schema{
	"v1.0.0": {
		"version":         &value[string]{"v1.0.0"},
		"logLevel":        &value[string]{"warn"},
		"host":            &value[string]{"localhost"},
		"port":            &value[int]{28080},
		"proxyPort":       &value[int]{0}, // 0 means no proxy
		"proxyTLS":        &value[bool]{true},
		"emailSender":     &value[string]{""},
		"emailPassword":   &value[string]{""},
		"ppVersion":       &value[int]{1},     // privacy policy version in use
		"newPpDate":       &value[string]{""}, // date new pp goes into effect, empty if none, RFC3339 format
		"updateNotify":    &value[bool]{true},
		"lastUpdateCheck": &value[string]{time.Now().Format(time.RFC3339)}, // time of last update check in RFC3339 format
		"updateAvailable": &value[bool]{false},
	},
	/*
		"v0.0.2": {
			"version": &value[string]{"v0.0.2"},
			"example1": &value[bool]{true},
			"example3": &value[ExampleV2]{ExampleV2{"value"}},
		},
		"v0.0.1": {
			"version": &value[string]{"v0.0.1"},
			"example1": &value[string]{"value"},
			"example2": &value[int]{0},
			"example3": &value[Example]{Example{1}},
		},
	*/
}
