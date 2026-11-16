package adi

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

// Fixed MID/OTP vectors detect changes independently of intermediate representations.
func TestTerminalOutputs(t *testing.T) {
	raw, err := os.ReadFile("testdata/outputs.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Input    string
		Counter  uint32
		Expected map[string]string
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 76 {
		t.Fatal("incomplete public regression vectors")
	}
	for i, v := range cases {
		input, err := hex.DecodeString(v.Input)
		if err != nil || len(input) != 400 {
			t.Fatal("invalid public input")
		}
		outputs := map[string]any{
			"MID":     provisioningMachineId([16]byte(input[:16])),
			"IDMSOTP": newIdmsOtp([400]byte(input), [32]byte(input[:32])).generate(v.Counter),
			"AMDOTP":  newAmdOtp([272]byte(input[:272])).generate(v.Counter),
		}
		if len(outputs) != len(v.Expected) {
			t.Fatal("incomplete expected outputs")
		}
		for name, got := range outputs {
			if fmt.Sprintf("%x", got) != v.Expected[name] {
				t.Fatalf("vector %d: %s changed", i, name)
			}
		}
	}
}
