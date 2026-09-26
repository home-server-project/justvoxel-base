package networking

// DeviceKind is the appliance-facing class of a NetworkManager device.
type DeviceKind string

const (
	DeviceKindEthernet DeviceKind = "ethernet"
	DeviceKindWiFi     DeviceKind = "wifi"
	DeviceKindOther    DeviceKind = "other"
)

// IPAddress is an address and prefix assigned to a device.
type IPAddress struct {
	Address string
	Prefix  uint32
}

// IPConfig is the effective runtime IP configuration reported by NetworkManager.
type IPConfig struct {
	Addresses []IPAddress
	Gateway   string
	DNS       []string
}

// ConnectionProfile contains the non-secret subset of a saved NetworkManager profile.
type ConnectionProfile struct {
	ID                  string
	UUID                string
	Type                string
	InterfaceName       string
	Autoconnect         bool
	AutoconnectPriority int32
	SSID                string
	Hidden              bool
	KeyManagement       string
	IPv4Method          string
	IPv6Method          string
}

// WirelessState contains runtime information for a Wi-Fi device.
type WirelessState struct {
	SSID        string
	Signal      uint8
	BitrateKbps uint32
}

// Device is the normalized appliance-facing view of a NetworkManager device.
type Device struct {
	Interface             string
	IPInterface           string
	Kind                  DeviceKind
	DeviceType            uint32
	State                 uint32
	Managed               bool
	HardwareAddress       string
	MTU                   uint32
	Carrier               *bool
	SpeedMbps             uint32
	IPv4                   IPConfig
	IPv6                   IPConfig
	ActiveConnection      *ConnectionProfile
	AvailableProfileUUIDs []string
	Wireless               *WirelessState
}

// Snapshot is a point-in-time read-only view of NetworkManager.
type Snapshot struct {
	Version           string
	State             uint32
	Connectivity      uint32
	NetworkingEnabled bool
	WirelessEnabled   bool
	Devices           []Device
	Profiles          []ConnectionProfile
}

// WiFiSecurity is the friendly security classification presented by JustVoxel.
type WiFiSecurity string

const (
	WiFiSecurityOpen         WiFiSecurity = "open"
	WiFiSecurityOWE          WiFiSecurity = "owe"
	WiFiSecurityPersonal     WiFiSecurity = "wpa-personal"
	WiFiSecurityWPA3Personal WiFiSecurity = "wpa3-personal"
	WiFiSecurityEnterprise   WiFiSecurity = "enterprise"
	WiFiSecurityWEP          WiFiSecurity = "wep"
	WiFiSecurityUnknown      WiFiSecurity = "unknown"
)

// WiFiNetwork is one scanned Wi-Fi network normalized for the appliance layer.
type WiFiNetwork struct {
	SSID           string
	BSSID          string
	Strength       uint8
	FrequencyMHz   uint32
	MaxBitrateKbps uint32
	Security       WiFiSecurity
	KeyManagement  string
	ProfileUUID    string
	Hidden         bool
	Known          bool
	Active         bool
}

// CheckpointDevice maps one appliance interface to NetworkManager's internal
// checkpoint device handle. The handle never crosses the Management API.
type CheckpointDevice struct {
	Interface string
	Handle    string
}

// Checkpoint is an internal handle for a NetworkManager safety checkpoint.
type Checkpoint struct {
	Handle                 string
	Devices                []CheckpointDevice
	RollbackTimeoutSeconds uint32
}

// RollbackResult is the normalized NetworkManager rollback result for a device.
type RollbackResult string

const (
	RollbackResultOK              RollbackResult = "ok"
	RollbackResultNoDevice        RollbackResult = "no-device"
	RollbackResultDeviceUnmanaged RollbackResult = "device-unmanaged"
	RollbackResultFailed          RollbackResult = "failed"
	RollbackResultUnknown         RollbackResult = "unknown"
)

// DeviceStateName converts NetworkManager's numeric device state to a stable API name.
func DeviceStateName(state uint32) string {
	switch state {
	case 10:
		return "unmanaged"
	case 20:
		return "unavailable"
	case 30:
		return "disconnected"
	case 40:
		return "prepare"
	case 50:
		return "config"
	case 60:
		return "need-auth"
	case 70:
		return "ip-config"
	case 80:
		return "ip-check"
	case 90:
		return "secondaries"
	case 100:
		return "activated"
	case 110:
		return "deactivating"
	case 120:
		return "failed"
	default:
		return "unknown"
	}
}

// ConnectivityName converts NetworkManager's connectivity state to a stable API name.
func ConnectivityName(state uint32) string {
	switch state {
	case 1:
		return "none"
	case 2:
		return "portal"
	case 3:
		return "limited"
	case 4:
		return "full"
	default:
		return "unknown"
	}
}


func rollbackResultName(result uint32) RollbackResult {
	switch result {
	case 0:
		return RollbackResultOK
	case 1:
		return RollbackResultNoDevice
	case 2:
		return RollbackResultDeviceUnmanaged
	case 3:
		return RollbackResultFailed
	default:
		return RollbackResultUnknown
	}
}

func deviceKindFromNM(deviceType uint32) DeviceKind {
	switch deviceType {
	case 1:
		return DeviceKindEthernet
	case 2:
		return DeviceKindWiFi
	default:
		return DeviceKindOther
	}
}
