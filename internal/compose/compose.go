// Package compose holds constants shared across stackd's compose-file discovery
// so every code path (stack application and the compose-file API) agrees on
// which file is "the" compose file for a stack.
package compose

// Candidates is the ordered list of compose filenames stackd recognises. The
// order is the discovery precedence: the first file that exists wins. A single
// shared list guarantees the operator is shown the same file that is applied.
var Candidates = []string{
	"compose.yaml", "compose.yml", "docker-compose.yml", "docker-compose.yaml",
}
