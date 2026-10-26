package main

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const instanceHelpMaxPages = 64
const instanceHelpMaxBytes = 65536
const instanceHelpMaxEntries = 512

type instanceConsoleCommand struct {
	Name        string `json:"name"`
	Usage       string `json:"usage,omitempty"`
	Description string `json:"description,omitempty"`
}
type instanceConsoleResponse struct {
	OK       bool                     `json:"ok"`
	ID       string                   `json:"id"`
	Output   string                   `json:"output"`
	Commands []instanceConsoleCommand `json:"commands,omitempty"`
	Partial  bool                     `json:"partial,omitempty"`
}

func normalizeInstanceConsoleCommand(raw string) (string, error) {
	// Validate the original input too: a newline must never become a second command.
	if len(raw) > 1024 || !utf8.ValidString(raw) {
		return "", instanceRuntimeFailure("Invalid console command.")
	}
	for _, character := range raw {
		if unicode.IsControl(character) || unicode.Is(unicode.Cf, character) {
			return "", instanceRuntimeFailure("Invalid console command.")
		}
	}
	command := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "/"))
	if command == "" {
		return "", instanceRuntimeFailure("Enter a Minecraft command.")
	}
	name := strings.ToLower(strings.Fields(command)[0])
	if name == "stop" || name == "minecraft:stop" {
		return "", instanceRuntimeFailure("Use normal server controls to stop the server.")
	}
	return command, nil
}

var instanceRCONSGR = regexp.MustCompile(`\x1b\[[0-9;:]*m`)
var instanceHelpColor = regexp.MustCompile(`§[0-9A-FK-ORXa-fk-orx]`)
var instanceHelpPages = regexp.MustCompile(`(?i)(?:\(|page\s+)([0-9]{1,4})\s*(?:/|of)\s*([0-9]{1,4})`)
var instanceHelpEntry = regexp.MustCompile(`^/?([A-Za-z0-9_][A-Za-z0-9_.-]*(?::[A-Za-z0-9_][A-Za-z0-9_.-]*)?)(.*)$`)

// Remove complete RCON formatting before diagnostic sanitization removes ESC.
// Otherwise ANSI color fragments remain visible and hide command-shaped lines.
func instanceRCONText(out []byte, secret string) string {
	text := instanceRCONSGR.ReplaceAll(out, nil)
	text = instanceHelpColor.ReplaceAll(text, nil)
	return instanceDiagnosticText(text, secret)
}

// Parse only command-shaped help lines. Unknown prose is not a command catalog.
func parseInstanceHelp(raw string) ([]instanceConsoleCommand, int) {
	entries := []instanceConsoleCommand{}
	pages := 1
	for _, line := range strings.Split(instanceHelpColor.ReplaceAllString(instanceRCONSGR.ReplaceAllString(raw, ""), ""), "\n") {
		line = strings.TrimSpace(line)
		if match := instanceHelpPages.FindStringSubmatch(line); match != nil {
			n, _ := strconv.Atoi(match[2])
			if n > pages {
				pages = n
			}
			continue
		}
		match := instanceHelpEntry.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		name, rest := match[1], strings.TrimSpace(match[2])
		entry := instanceConsoleCommand{Name: name}
		switch {
		case strings.HasPrefix(rest, ":"):
			entry.Description = strings.TrimSpace(strings.TrimPrefix(rest, ":"))
		case strings.HasPrefix(rest, "-"):
			entry.Description = strings.TrimSpace(strings.TrimPrefix(rest, "-"))
		case strings.HasPrefix(line, "/"), strings.HasPrefix(rest, "<"), strings.HasPrefix(rest, "["):
			parts := strings.SplitN(rest, " - ", 2)
			entry.Usage = strings.TrimSpace(name + " " + parts[0])
			if len(parts) == 2 {
				entry.Description = strings.TrimSpace(parts[1])
			}
		default:
			continue
		}
		entries = append(entries, entry)
		if len(entries) >= instanceHelpMaxEntries {
			break
		}
	}
	return entries, pages
}

// Each page retains the ordinary 5-second / 8 KiB RCON limits. The caller also
// supplies one 20-second deadline shared by observation, all pages and audit.
func (b *instanceRuntimeBackend) discoverCommands(ctx context.Context, id, secret string) ([]instanceConsoleCommand, bool, error) {
	entries := []instanceConsoleCommand{}
	seen := map[string]bool{}
	responses := map[string]bool{}
	pages, total := 1, 0
	partial := false
	for page := 1; page <= pages && page <= instanceHelpMaxPages; page++ {
		if ctx.Err() != nil {
			return entries, true, nil
		}
		command := "help"
		if page > 1 {
			command += " " + strconv.Itoa(page)
		}
		out, err := b.rcon(ctx, id, command)
		if err != nil {
			if page == 1 {
				return nil, false, err
			}
			return entries, true, nil
		}
		total += len(out)
		if total > instanceHelpMaxBytes {
			return entries, true, nil
		}
		text := instanceRCONText(out, secret)
		if responses[text] {
			return entries, true, nil
		}
		responses[text] = true
		found, count := parseInstanceHelp(text)
		if count > pages {
			pages = count
		}
		if pages > instanceHelpMaxPages {
			partial = true
		}
		for _, entry := range found {
			if seen[entry.Name] {
				continue
			}
			seen[entry.Name] = true
			entries = append(entries, entry)
			if len(entries) >= instanceHelpMaxEntries {
				return entries, true, nil
			}
		}
	}
	return entries, partial, nil
}
