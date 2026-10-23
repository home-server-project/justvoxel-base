package main

import (
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSpigotProviderCapabilitySeparation(t *testing.T) {
	if !reflect.DeepEqual(pluginLoaders("spigot"), []string{"spigot", "bukkit"}) {
		t.Fatal("Spigot provider mapping")
	}
	if _, err := hangarPluginPlatform("spigot"); err == nil {
		t.Fatal("PAPER is not Spigot evidence")
	}
	for _, software := range []string{"vanilla", "forge", "fabric", "neoforge", "quilt", "bedrock"} {
		if javaServerPluginsWorkspace(software) || len(pluginLoaders(software)) != 0 {
			t.Fatalf("Bukkit workspace exposed for %s", software)
		}
	}
	server := pluginServerContext{ServerType: "spigot", MinecraftVersion: "1.21.8", Supported: true}
	for _, tc := range []struct {
		loader, game string
		compatible   bool
	}{
		{"spigot", "1.21.8", true}, {"bukkit", "1.21.8", true}, {"paper", "1.21.8", false}, {"spigot", "1.21.7", false},
	} {
		if pluginVersionCompatible(pluginCatalogVersion{Loaders: []string{tc.loader}, GameVersions: []string{tc.game}}, server) != tc.compatible {
			t.Fatalf("%+v", tc)
		}
	}
}

func TestSpigotRejectsPaperOnlyArtifact(t *testing.T) {
	for _, descriptor := range []string{"paper-plugin.yml", "plugin.yml"} {
		path := filepath.Join(t.TempDir(), "plugin.jar")
		pluginTestJar(t, path, map[string]string{descriptor: "name: Example\nmain: example.Main\nversion: 1.0\n"})
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		_, err = inspectServerPlugin(file, "plugin.jar", "spigot")
		file.Close()
		if (err == nil) != (descriptor == "plugin.yml") {
			t.Fatalf("%s: %v", descriptor, err)
		}
	}
}

func TestManagedCrossplayPortsAndCapabilities(t *testing.T) {
	for _, software := range []string{"paper", "purpur", "spigot", "fabric", "neoforge", "vanilla", "forge", "quilt", "bedrock"} {
		t.Run(software, func(t *testing.T) {
			store, ctx, configs := instanceRuntimeFixture(t)
			c := &configs[0]
			c.Software, c.Version, c.VersionPolicy = software, "1.21.8", "pinned"
			c.BedrockEnabled, c.BedrockPort = true, 19132
			switch software {
			case "spigot":
				c.ImageTag = "java21-jdk"
			case "fabric":
				c.ImageTag, c.LoaderVersion = "java21", "0.16.14"
			case "neoforge":
				c.ImageTag, c.LoaderVersion = "java21", "21.8.1"
			}
			supported := software == "paper" || software == "purpur" || software == "spigot" || software == "fabric" || software == "neoforge"
			if instanceCrossplayValid(*c) != supported {
				t.Fatal("cross-play capability")
			}
			files, err := store.renderInstanceRuntimes(ctx, configs)
			if (err == nil) != supported {
				t.Fatalf("runtime: %v", err)
			}
			if !supported {
				return
			}
			for _, want := range []string{"PublishPort=19132:19132/tcp", "PublishPort=19132:19132/udp"} {
				if !strings.Contains(string(files[0].Quadlet), want) {
					t.Fatal(want)
				}
			}
			for _, proto := range []string{"tcp", "udp"} {
				if !instanceRuleForRuntime(instanceFirewallRule{Port: 19132, Protocol: proto}, *c) {
					t.Fatal("missing firewall protocol", proto)
				}
			}
			configs[1].JavaPort = c.BedrockPort
			if _, err := store.renderInstanceRuntimes(ctx, configs); err == nil {
				t.Fatal("Geyser TCP collided with another Java server")
			}
			configs[1].JavaPort = 25567
			configs[1].Software, configs[1].Version, configs[1].ImageTag = "purpur", "1.21.8", "stable"
			configs[1].BedrockEnabled, configs[1].BedrockPort = true, c.BedrockPort
			if _, err := store.renderInstanceRuntimes(ctx, configs); err == nil {
				t.Fatal("two Geyser reservations share TCP/UDP")
			}
			configs[1].BedrockEnabled, configs[1].BedrockPort = false, 0
			c.BedrockPort = c.JavaPort
			if _, err := store.renderInstanceRuntimes(ctx, configs); err == nil {
				t.Fatal("Java and Geyser share TCP port")
			}
		})
	}
}

func TestGeyserPortRejectsTCPAndUDPCollisions(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if held, err := instanceHoldCrossplayPort(port); err == nil {
		held.Close()
		t.Fatal("TCP collision accepted")
	}
	listener.Close()
	packet, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer packet.Close()
	port = packet.LocalAddr().(*net.UDPAddr).Port
	if held, err := instanceHoldCrossplayPort(port); err == nil {
		held.Close()
		t.Fatal("UDP collision accepted")
	}
}

// Separate protocols may share a number until managed Geyser reserves both.
func TestUnrelatedUDPDoesNotReserveJavaTCP(t *testing.T) {
	packet, err := net.ListenPacket("udp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer packet.Close()
	port := packet.LocalAddr().(*net.UDPAddr).Port
	listeners, err := instanceHoldPort(port)
	if err != nil {
		t.Fatal("unrelated UDP blocked Java TCP", err)
	}
	for _, listener := range listeners {
		listener.Close()
	}
	if held, err := instanceHoldCrossplayPort(port); err == nil {
		held.Close()
		t.Fatal("Geyser ignored the UDP reservation")
	}
}
