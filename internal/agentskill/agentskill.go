// Package agentskill carries the "erbrus" skill: how to work as an erbrus
// agent and how to configure a CLI as an erbrus provider. The content is
// compiled in; installed copies are derived and overwritten whole.
package agentskill

import (
	_ "embed"
	"fmt"
	"regexp"
	"strconv"
)

//go:embed erbrus.md
var body string

// Version is bumped whenever erbrus.md changes; installed copies carry it
// so `erbrus agents setup` can tell new / outdated / up to date apart.
const Version = 2

const (
	Name        = "erbrus"
	Description = "Use when running as an erbrus agent (report to your channel with erbrus msg send, reply to forwards) or when configuring an AI coding CLI as an erbrus provider (command template, prompt delivery, screen rules)."
)

// Body is the canonical markdown, no frontmatter, no marker.
func Body() string { return body }

func marker() string { return fmt.Sprintf("<!-- erbrus:erbrus:v%d -->", Version) }

// SkillFile renders the SKILL.md every supported CLI reads: frontmatter,
// version marker, body.
func SkillFile() string {
	return "---\nname: " + Name + "\ndescription: " + Description + "\n---\n\n" + marker() + "\n\n" + body
}

var versionRe = regexp.MustCompile(`erbrus:erbrus:v(\d+)`)

// HasMarker reports whether content is an erbrus-installed skill (any version).
func HasMarker(content []byte) bool { return versionRe.Match(content) }

// InstalledVersion returns the stamped version, or -1 without a marker.
func InstalledVersion(content []byte) int {
	m := versionRe.FindSubmatch(content)
	if m == nil {
		return -1
	}
	n, err := strconv.Atoi(string(m[1]))
	if err != nil {
		return -1
	}
	return n
}
