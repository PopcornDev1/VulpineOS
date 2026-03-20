package remote

import "encoding/json"

// Envelope wraps messages sent over the WebSocket relay.
type Envelope struct {
	Type    string          `json:"type"`    // "juggler", "control", "tui_state"
	Payload json.RawMessage `json:"payload"`
}

// NewJugglerEnvelope wraps a Juggler protocol message.
func NewJugglerEnvelope(data []byte) ([]byte, error) {
	env := Envelope{
		Type:    "juggler",
		Payload: json.RawMessage(data),
	}
	return json.Marshal(env)
}

// NewControlEnvelope wraps a control command.
func NewControlEnvelope(command string, params interface{}) ([]byte, error) {
	payload, err := json.Marshal(map[string]interface{}{
		"command": command,
		"params":  params,
	})
	if err != nil {
		return nil, err
	}
	env := Envelope{
		Type:    "control",
		Payload: json.RawMessage(payload),
	}
	return json.Marshal(env)
}
