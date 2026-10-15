package main

import (
	"io"
	"net"
	"strconv"
)

func instanceCrossplayValid(c minecraftInstanceRuntimeConfig) bool {
	if !c.BedrockEnabled {
		return c.BedrockPort == 0
	}
	return (c.Software == "paper" || c.Software == "purpur") && c.BedrockPort > 0 && c.BedrockPort <= 65535
}

// An omitted protocol is the Phase 3A TCP ownership format.
func (r instanceFirewallRule) protocol() string {
	if r.Protocol == "" {
		return "tcp"
	}
	return r.Protocol
}
func instanceRuleForRuntime(r instanceFirewallRule, c minecraftInstanceRuntimeConfig) bool {
	return (r.protocol() == "tcp" && r.Port == c.JavaPort) || (r.protocol() == "udp" && c.BedrockEnabled && r.Port == c.BedrockPort)
}
func instanceHoldUDP(port int) (net.PacketConn, error) {
	conn, err := net.ListenPacket("udp", ":"+strconv.Itoa(port))
	if err != nil {
		return nil, instanceRuntimeFailure("Bedrock UDP port is already occupied or unavailable.")
	}
	return conn, nil
}
func instanceHoldFirewallPort(r instanceFirewallRule) ([]io.Closer, error) {
	if r.protocol() == "udp" {
		conn, err := instanceHoldUDP(r.Port)
		if err != nil {
			return nil, err
		}
		return []io.Closer{conn}, nil
	}
	listeners, err := instanceHoldPort(r.Port)
	if err != nil {
		return nil, err
	}
	result := []io.Closer{}
	for _, l := range listeners {
		result = append(result, l)
	}
	return result, nil
}
