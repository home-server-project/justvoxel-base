package main

import (
	"errors"
	"regexp"
)

// Native, workspace and managed cross-play capabilities are independent.
type javaServerCapabilities struct {
	Type              string
	Name              string
	BukkitPlugins     bool
	ModLoader         bool
	PluginsWorkspace  bool
	ManagedCrossplay  bool
	LoaderEnvironment string
}

func javaServerSoftware(software string) (javaServerCapabilities, bool) {
	switch software {
	case "paper":
		return javaServerCapabilities{Type: "PAPER", Name: "Paper", BukkitPlugins: true, PluginsWorkspace: true, ManagedCrossplay: true}, true
	case "purpur":
		return javaServerCapabilities{Type: "PURPUR", Name: "Purpur", BukkitPlugins: true, PluginsWorkspace: true, ManagedCrossplay: true}, true
	case "vanilla":
		return javaServerCapabilities{Type: "VANILLA", Name: "Vanilla"}, true
	case "spigot":
		return javaServerCapabilities{Type: "SPIGOT", Name: "Spigot", BukkitPlugins: true, PluginsWorkspace: true, ManagedCrossplay: true}, true
	case "forge":
		return javaServerCapabilities{Type: "FORGE", Name: "Forge", ModLoader: true, LoaderEnvironment: "FORGE_VERSION"}, true
	case "neoforge":
		return javaServerCapabilities{Type: "NEOFORGE", Name: "NeoForge", ManagedCrossplay: true, ModLoader: true, LoaderEnvironment: "NEOFORGE_VERSION"}, true
	case "fabric":
		return javaServerCapabilities{Type: "FABRIC", Name: "Fabric", ManagedCrossplay: true, ModLoader: true, LoaderEnvironment: "FABRIC_LOADER_VERSION"}, true
	case "quilt":
		return javaServerCapabilities{Type: "QUILT", Name: "Quilt", ModLoader: true, LoaderEnvironment: "QUILT_LOADER_VERSION"}, true
	}
	return javaServerCapabilities{}, false
}

func javaServerPluginsWorkspace(software string) bool {
	c, ok := javaServerSoftware(software)
	return ok && c.PluginsWorkspace
}

func javaServerAllowsSwitch(from, to string) bool {
	_, a := javaServerSoftware(from)
	_, b := javaServerSoftware(to)
	return a && b && (from == to || (from == "paper" || from == "purpur") && (to == "paper" || to == "purpur"))
}

var javaLoaderVersionPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+){1,3}(-[A-Za-z0-9]+([.-][A-Za-z0-9]+)*)?$`)
var java21GameReleasePattern = regexp.MustCompile(`^1\.(20\.[56]|21(\.([0-9]|1[01]))?)$`)
var java25GameReleasePattern = regexp.MustCompile(`^26\.[1-9][0-9]*(\.(0|[1-9][0-9]*))?$`)

// Existing types retain their deployed policies and Java image choices. New
// types require explicit pins and the Java generation for the game release.
// Exact upstream game/loader verification is still required before activation.
func validateJavaServerSelection(software, policy, version, loader, image string, bedrock bool) error {
	c, ok := javaServerSoftware(software)
	if !ok || bedrock && !c.ManagedCrossplay {
		return errors.New("unsupported Java server capabilities")
	}
	if !c.ModLoader && loader != "" {
		return errors.New("this server software does not accept a loader version")
	}
	if software == "paper" || software == "purpur" || software == "vanilla" {
		return nil
	}
	if policy != "pinned" {
		return errors.New("new Java server types require a pinned Minecraft release")
	}
	imageTag := "java21"
	switch {
	case java21GameReleasePattern.MatchString(version):
	case java25GameReleasePattern.MatchString(version):
		imageTag = "java25"
	default:
		return errors.New("new Java server types require Minecraft 1.20.5–1.21.11 or 26.x")
	}
	if software == "spigot" {
		imageTag += "-jdk"
	}
	if image != imageTag {
		return errors.New("selected Minecraft release and server software require the " + imageTag + " image tag")
	}
	if c.ModLoader && (len(loader) > 64 || !javaLoaderVersionPattern.MatchString(loader)) {
		return errors.New("an exact loader version is required")
	}
	return nil
}

func javaServerCrossplay(software string) bool {
	c, ok := javaServerSoftware(software)
	return ok && c.ManagedCrossplay
}

func javaServerManagedViaVersion(software string) bool {
	switch software {
	case "paper", "purpur", "spigot":
		return true
	}
	return false
}
