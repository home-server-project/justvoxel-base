package main

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// RCON returns the command response directly, never log lines or player chat.
// Stay below the trusted command runner's capture limit so truncated output
// cannot become a complete inventory. Count and identity bounds are independent.
const spigotPluginListLimit = 32 << 10

var spigotPluginListHeader = regexp.MustCompile(`^Plugins \(([0-9]{1,4})\): ?(.*)$`)
var spigotFormatting = regexp.MustCompile(`^(?:§[0-9a-fk-orA-FK-OR]|\x1b\[(?:[0-9]{1,2})(?:;[0-9]{1,2}){0,3}m)`)

// Only boundary decorations are accepted. Removing formatting inside a name
// could manufacture an identity that the server never actually reported.
// Red denotes disabled Bukkit plugins; other nonstandard colors fail closed.
func spigotDecoratedName(raw string, active bool) (name string, enabled, nextActive, valid bool) {
	suffix := false
	for len(raw) > 0 {
		if code := spigotFormatting.FindString(raw); code != "" {
			color, ok := spigotPluginDecoration(code)
			if !ok {
				return "", false, false, false
			}
			if color != 0 {
				active = color > 0
			}
			if name != "" {
				suffix = true
			}
			raw = raw[len(code):]
			continue
		}
		if suffix || len(name) >= 128 {
			return "", false, false, false
		}
		if name == "" {
			enabled = active
		}
		name += raw[:1]
		raw = raw[1:]
	}
	return strings.ToLower(name), enabled, active, pluginNamePattern.MatchString(name)
}

// Accept only Bukkit's enabled/disabled colors and bounded SGR equivalents.
// Return the final color transition (-1 disabled, 1 enabled/default, 0 unchanged).
// A combined SGR sequence is interpreted in order, never blindly stripped.
func spigotPluginDecoration(code string) (color int, valid bool) {
	switch strings.ToLower(code) {
	case "§c":
		return -1, true
	case "§a", "§r", "§f", "§7":
		return 1, true
	case "§l", "§m", "§n", "§o", "§k":
		return 0, true
	}
	if !strings.HasPrefix(code, "\x1b[") {
		return 0, false
	}
	for _, parameter := range strings.Split(code[2:len(code)-1], ";") {
		switch parameter {
		case "31", "91":
			color = -1
		case "0", "39", "32", "92", "37", "97":
			color = 1
		case "1", "22":
		default:
			return 0, false
		}
	}
	return color, true
}

func spigotPluginNamesLoaded(raw []byte, expected []string) bool {
	if len(raw) == 0 || len(raw) >= spigotPluginListLimit || !utf8.Valid(raw) || len(expected) == 0 || len(expected) > pluginMaxArtifacts {
		return false
	}
	// One inventory only. Permit a single transport line ending, never extra
	// messages, repeated headers, embedded controls or multiline player content.
	line := strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")
	if strings.ContainsAny(line, "\r\n\x00") {
		return false
	}
	// Bukkit may color the header. Its fixed grammar cannot supply identities.
	headerEnd := strings.Index(line, ":")
	if headerEnd < 0 {
		return false
	}
	header := line[:headerEnd+1]
	var plain strings.Builder
	for len(header) > 0 {
		if code := spigotFormatting.FindString(header); code != "" {
			// A red header is not ordinary Bukkit inventory output. Do not
			// lose a disabled color state while decoding its fixed grammar.
			if color, valid := spigotPluginDecoration(code); !valid || color < 0 {
				return false
			}
			header = header[len(code):]
		} else {
			plain.WriteByte(header[0])
			header = header[1:]
		}
	}
	match := spigotPluginListHeader.FindStringSubmatch(plain.String() + line[headerEnd+1:])
	if match == nil {
		return false
	}
	count, err := strconv.Atoi(match[1])
	if err != nil || count < 1 || count > 4096 || strconv.Itoa(count) != match[1] {
		return false
	}
	entries := strings.Split(match[2], ", ")
	if len(entries) != count {
		return false
	}
	inventory := map[string]bool{}
	seen := map[string]bool{}
	active := true
	for _, entry := range entries {
		name, enabled, nextActive, valid := spigotDecoratedName(entry, active)
		active = nextActive
		if !valid || seen[name] {
			return false
		}
		seen[name], inventory[name] = true, enabled
	}
	wanted := map[string]bool{}
	for _, name := range expected {
		normalized := strings.ToLower(name)
		if !pluginNamePattern.MatchString(name) || wanted[normalized] || !inventory[normalized] {
			return false
		}
		wanted[normalized] = true
	}
	return true
}
