package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// CurrentSchemaVersion is the schema version the framework writes for new
// configs. Bump this when introducing a new on-disk shape.
const CurrentSchemaVersion = 1

// MinSupportedSchemaVersion is the oldest schema version this binary will
// accept. Keep this one behind Current for one release cycle, then bump to
// retire the old shape. Files outside [Min, Current] are rejected at load.
const MinSupportedSchemaVersion = 1

// schemaVersionProbe peeks at a YAML file for its top-level schema_version key
// without doing a full unmarshal. A missing field is treated as version 1 so
// existing configs continue to load until they are migrated.
type schemaVersionProbe struct {
	SchemaVersion *int `yaml:"schema_version"`
}

// checkSchemaVersion returns nil if the file's declared schema_version is in
// the supported range, or a descriptive error otherwise. The error names the
// file so operators can find the offending config quickly.
func checkSchemaVersion(file string, data []byte) error {
	var probe schemaVersionProbe
	if err := yaml.Unmarshal(data, &probe); err != nil {
		return fmt.Errorf("%s: failed to read schema_version: %w", file, err)
	}
	v := CurrentSchemaVersion
	if probe.SchemaVersion != nil {
		v = *probe.SchemaVersion
	}
	if v < MinSupportedSchemaVersion || v > CurrentSchemaVersion {
		return fmt.Errorf(
			"%s: schema_version %d not supported by this binary (supported range: %d..%d). "+
				"Update the file or deploy a framework version that accepts it",
			file, v, MinSupportedSchemaVersion, CurrentSchemaVersion,
		)
	}
	return nil
}
