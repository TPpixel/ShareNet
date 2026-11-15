package adb

import "testing"

func TestParseDevicesOutput(t *testing.T) {
	raw := `List of devices attached
R5CX123456A            device product:dm3qxx model:SM_S918B device:dm3q transport_id:1
emulator-5554          unauthorized transport_id:2
* daemon started successfully
`
	devs := ParseDevicesOutput(raw)
	if len(devs) != 2 {
		t.Fatalf("got %d devices, want 2: %+v", len(devs), devs)
	}
	if devs[0].Serial != "R5CX123456A" || devs[0].State != "device" || devs[0].Model != "SM S918B" {
		t.Fatalf("unexpected first device: %+v", devs[0])
	}
	if devs[1].Serial != "emulator-5554" || devs[1].State != "unauthorized" {
		t.Fatalf("unexpected second device: %+v", devs[1])
	}
}

func TestParseHotspotStatus(t *testing.T) {
	tethering := `Tethering:
  wlan2 - TetheredState - UpstreamWanted - AutoRestart
`
	ipAddr := `1: lo: <LOOPBACK,UP>
    inet 127.0.0.1/8 scope host lo
28: wlan2: <BROADCAST,MULTICAST,UP,LOWER_UP>
    inet 192.168.43.1/24 brd 192.168.43.255 scope global wlan2
`
	st := ParseHotspotStatus(tethering, ipAddr)
	if !st.IsActive || st.InterfaceName != "wlan2" || st.IpAddress != "192.168.43.1" {
		t.Fatalf("unexpected hotspot status from tethered iface: %+v", st)
	}

	// Fallback prefix matching when dumpsys output does not show TetheredState
	ipFallback := `15: swlan0: <BROADCAST,UP>
    inet 192.168.137.1/24 brd 192.168.137.255 scope global swlan0
`
	st2 := ParseHotspotStatus("", ipFallback)
	if !st2.IsActive || st2.InterfaceName != "swlan0" || st2.IpAddress != "192.168.137.1" {
		t.Fatalf("unexpected fallback hotspot status: %+v", st2)
	}
}
