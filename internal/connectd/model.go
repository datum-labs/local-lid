package connectd

import "encoding/json"

// Status mirrors ProjectStatus in api.rs. networking, transport and networks
// are untyped JSON on the daemon side, so they stay raw here.
type Status struct {
	Networking           json.RawMessage `json:"networking"`
	Transport            json.RawMessage `json:"transport"` // null when the project isn't running
	Project              string          `json:"project"`
	DesiredUp            bool            `json:"desired_up"`
	Running              bool            `json:"running"`
	Enrolled             bool            `json:"enrolled"`
	CredentialConfigured bool            `json:"credential_configured"`
	Authentication       *Authentication `json:"authentication"`
	Connector            *Connector      `json:"connector"`
	Services             []Service       `json:"services"`
	Dials                []Dial          `json:"dials"`
	Networks             json.RawMessage `json:"networks"`
	LastError            *string         `json:"last_error"`
	LastErrorStage       *string         `json:"last_error_stage"`
}

type Authentication struct {
	Kind    string  `json:"kind"`
	Session *string `json:"session"`
}

type Connector struct {
	Name      string `json:"name"`
	UID       string `json:"uid"`
	PublicKey string `json:"public_key"`
}

type Service struct {
	ID             string   `json:"id"`
	Endpoint       string   `json:"endpoint"`
	Protocol       string   `json:"protocol"`
	Public         bool     `json:"public"`
	Hostname       *string  `json:"hostname"`
	Allow          []string `json:"allow"`
	DesiredActive  bool     `json:"desired_active"`
	Running        bool     `json:"running"`
	Ready          bool     `json:"ready"`
	Hostnames      []string `json:"hostnames"`
	LastError      *string  `json:"last_error"`
	LastErrorStage *string  `json:"last_error_stage"`
	LastActor      string   `json:"last_actor"`
}

type Dial struct {
	Connector      string  `json:"connector"`
	ConnectorName  *string `json:"connector_name"`
	Port           uint16  `json:"port"`
	Bind           uint16  `json:"bind"`
	LocalPort      *uint16 `json:"local_port"`
	Protocol       string  `json:"protocol"`
	DesiredActive  bool    `json:"desired_active"`
	Running        bool    `json:"running"`
	LastError      *string `json:"last_error"`
	LastErrorStage *string `json:"last_error_stage"`
	LastActor      string  `json:"last_actor"`
}
