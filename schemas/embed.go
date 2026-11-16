// Package schemas embeds the JSON Schema files that define the WxAlerts PWS
// MQTT contract so that Go code validates against the same documents that
// are published for other implementations. There is one copy of each schema
// in the repository: the files in this directory.
package schemas

import "embed"

// FS holds every *.schema.json file in this directory. File names are the
// last path segment of the schema's $id, for example "obs.schema.json" for
// https://schemas.wxalerts.org/pws/v0/obs.schema.json.
//
//go:embed *.schema.json
var FS embed.FS

// BaseURL is the $id prefix shared by every schema in FS. A schema's $id is
// BaseURL plus its file name.
const BaseURL = "https://schemas.wxalerts.org/pws/v0/"
