package driversdk

import "testing"

// The mapping that lets every driver written against DriverType keep working
// unchanged while the platform moves to roles.
func TestRolesForDerivesFromLegacyType(t *testing.T) {
	cases := []struct {
		in           DriverType
		wantDevice   bool
		wantUI       bool
		wantTopology DeviceTopology
	}{
		{DriverTypeDevice, true, false, DeviceTopologyDirect},
		{DriverTypeHub, true, false, DeviceTopologyHub},
		{DriverTypeChild, true, false, DeviceTopologyChild},
		{DriverTypeUI, false, true, ""},
		{"ui", false, true, ""},
		{"", true, false, DeviceTopologyDirect},
	}

	for _, tc := range cases {
		got := RolesFor(tc.in)
		if got.HasDevice() != tc.wantDevice || got.HasUI() != tc.wantUI {
			t.Errorf("%q: device=%v ui=%v, want device=%v ui=%v",
				tc.in, got.HasDevice(), got.HasUI(), tc.wantDevice, tc.wantUI)
		}
		if got.Topology() != tc.wantTopology {
			t.Errorf("%q: topology %q, want %q", tc.in, got.Topology(), tc.wantTopology)
		}
	}
}

// Round-tripping must not invent a role. Anything that came from a legacy type
// renders back as the same legacy type.
func TestLegacyRoundTrip(t *testing.T) {
	for _, in := range []DriverType{DriverTypeDevice, DriverTypeHub, DriverTypeChild, DriverTypeUI} {
		if got := RolesFor(in).LegacyDriverType(); got != in {
			t.Errorf("%q round-tripped to %q", in, got)
		}
	}
}

// The case the enum could not hold. Reporting DEVICE here is lossy on purpose —
// it is what an unmigrated consumer expects — but both roles are still present
// for anything that reads them properly.
func TestDualRoleIsLossyOnlyInTheLegacyRendering(t *testing.T) {
	r := Roles{Device: &DeviceRole{Topology: DeviceTopologyDirect}, UI: &UIRole{Surface: "media_player"}}

	if !r.HasDevice() || !r.HasUI() {
		t.Fatal("a dual-role driver lost a role")
	}
	if got := r.LegacyDriverType(); got != DriverTypeDevice {
		t.Errorf("legacy rendering is %q, want DEVICE", got)
	}
}

func TestPublisherClassRank(t *testing.T) {
	if PublisherSafety.Rank() <= PublisherManual.Rank() {
		t.Error("a fire alarm does not outrank a keypad")
	}
	if PublisherManual.Rank() <= PublisherAutomation.Rank() {
		t.Error("a person does not outrank a schedule")
	}
	if PublisherAutomation.Rank() <= PublisherSensor.Rank() {
		t.Error("a scene does not outrank an occupancy sensor")
	}
	// A typo must land on the default rather than losing every contest.
	if got := PublisherClass("TYPO").Rank(); got != PublisherAutomation.Rank() {
		t.Errorf("an unrecognised class ranks %d, want the AUTOMATION default %d",
			got, PublisherAutomation.Rank())
	}
	if got := PublisherClass("  safety ").Rank(); got != 4 {
		t.Errorf("case and padding changed the rank: got %d", got)
	}
}

// A driver that says nothing about roles still gets the right ones, which is
// what makes RoledDriver safe to add without touching the Driver interface.
type legacyOnlyDriver struct{ Driver }

func (legacyOnlyDriver) Type() DriverType { return DriverTypeUI }

type roleDeclaringDriver struct{ Driver }

func (roleDeclaringDriver) Type() DriverType { return DriverTypeDevice }
func (roleDeclaringDriver) Roles() Roles {
	return Roles{Device: &DeviceRole{}, UI: &UIRole{Surface: "media_player"}}
}

func TestRolesOfPrefersADeclarationAndFallsBack(t *testing.T) {
	if got := RolesOf(legacyOnlyDriver{}); !got.HasUI() || got.HasDevice() {
		t.Errorf("a legacy UI driver resolved to %+v, want ui only", got)
	}

	got := RolesOf(roleDeclaringDriver{})
	if !got.HasDevice() || !got.HasUI() {
		t.Errorf("a declaring driver resolved to %+v, want both roles", got)
	}
}
