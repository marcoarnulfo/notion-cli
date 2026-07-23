package config

import (
	"fmt"
	"os"
)

// migrate brings an on-disk config up to CurrentSchemaVersion.
//
// A missing version means a file written before versioning existed. A version
// from the future means the user downgraded notion-track: warn, then carry on
// reading what we understand rather than refusing to work.
func migrate(c *Config) {
	switch {
	case c.SchemaVersion == 0:
		c.SchemaVersion = CurrentSchemaVersion
	case c.SchemaVersion > CurrentSchemaVersion:
		fmt.Fprintf(os.Stderr,
			"warning: config schema version %d is newer than this build understands (%d); "+
				"some settings may be ignored\n",
			c.SchemaVersion, CurrentSchemaVersion)
	}
}
