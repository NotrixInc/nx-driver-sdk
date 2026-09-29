package driversdk

import (
	"encoding/json"
	"testing"
)

// The controller's AV resolver sends exactly this shape (see
// controller-core internal/httpserver/ui_av.go, busCommander).
func TestCapabilityCommandsDecode(t *testing.T) {
	var batch EndpointBatch
	if err := json.Unmarshal([]byte(`{
		"kind": "command", "name": "select", "source_driver_id": "core.av",
		"command_class": "ABSOLUTE",
		"items": [{"endpoint_key": "hdmi_in3",
		           "params": {"cap": "cap.av_input@v1", "instance": "main_input", "command": "select", "value": "hdmi_in3"}}]
	}`), &batch); err != nil {
		t.Fatal(err)
	}
	cmds := batch.CapabilityCommands()
	if len(cmds) != 1 {
		t.Fatalf("got %d commands", len(cmds))
	}
	c := cmds[0]
	if !c.Is("cap.av_input", "select") || c.Instance != "main_input" || c.EndpointKey != "hdmi_in3" {
		t.Errorf("decoded %+v", c)
	}
	if v, ok := c.String(); !ok || v != "hdmi_in3" {
		t.Errorf("value %q", v)
	}

	// A binding crossing is not a capability command.
	var crossing EndpointBatch
	_ = json.Unmarshal([]byte(`{"kind":"command","name":"set_level","items":[{"endpoint_key":"ch1","params":{"level":40}}]}`), &crossing)
	if len(crossing.CapabilityCommands()) != 0 {
		t.Error("a binding crossing decoded as a resolver command")
	}

	// Leaving a group is a select of nothing.
	var leave EndpointBatch
	_ = json.Unmarshal([]byte(`{"kind":"command","name":"select","items":[{"endpoint_key":"speakers","params":{"cap":"cap.av_input@v1","command":"select","value":null}}]}`), &leave)
	if v, ok := leave.CapabilityCommands()[0].String(); !ok || v != "" {
		t.Errorf("null select read as %q, %v", v, ok)
	}
}
