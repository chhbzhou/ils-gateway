package config

import "testing"

func TestParseAndValidate(t *testing.T) {
	c, e := ParseUCI([]byte("enabled=true\ndevice_mac=02:00:00:00:00:01\nmax_connections=8\nmax_body_bytes=4096\nmax_buffered_bytes=16388\n"))
	if e != nil || !c.Enabled || c.MaxConnections != 8 {
		t.Fatalf("%+v %v", c, e)
	}
}
func TestRejectsInvalid(t *testing.T) {
	if _, e := ParseUCI([]byte("max_body_bytes=1")); e == nil {
		t.Fatal("expected error")
	}
}
func TestParsesOpenWrtUCI(t *testing.T) {
	c, e := ParseUCI([]byte("config ios_location_spoofer 'main'\noption enabled '1'\nlist device_macs '02:00:00:00:00:01'\noption max_connections '8'\noption max_body_bytes '4096'\noption max_buffered_bytes '16388'\nlist wloc_host 'gsp-ssl.ls.apple.com'\nconfig unrelated 'other'\noption enabled '0'\nconfig profile 'location'\noption latitude '35.5'\noption longitude '139.7'\n"))
	if e != nil || !c.Enabled || c.MaxConnections != 8 {
		t.Fatalf("%+v %v", c, e)
	}
	if !c.ProfileSet || c.Profile.Latitude != 35.5 || c.Profile.Longitude != 139.7 {
		t.Fatalf("profile was not parsed: %+v", c.Profile)
	}
}

func TestEnabledRequiresDeviceMACOrIP(t *testing.T) {
	if _, err := ParseUCI([]byte("enabled=true\n")); err == nil {
		t.Fatal("enabled configuration without a device selector was accepted")
	}
	if _, err := ParseUCI([]byte("enabled=false\n")); err != nil {
		t.Fatalf("disabled configuration should allow empty device selectors: %v", err)
	}
}

func TestRejectsInvalidDeviceAndProfile(t *testing.T) {
	if _, e := ParseUCI([]byte("config ios_location_spoofer 'main'\noption device_mac 'bad'\n")); e == nil {
		t.Fatal("invalid MAC accepted")
	}
	if _, e := ParseUCI([]byte("config ios_location_spoofer 'main'\nconfig profile 'location'\noption latitude '1'\n")); e == nil {
		t.Fatal("partial profile accepted")
	}
	if _, e := ParseUCI([]byte("config ios_location_spoofer 'main'\noption listen_port '0'\n")); e == nil {
		t.Fatal("invalid port accepted")
	}
}

func TestRejectsNonDefaultListenerConfiguration(t *testing.T) {
	for _, input := range []string{
		"listen_addr=127.0.0.1\n",
		"listen_addr=::\n",
		"listen_port=11443\n",
		"proxy_port=11445\n",
	} {
		if _, err := ParseUCI([]byte(input)); err == nil {
			t.Fatalf("accepted non-default listener configuration %q", input)
		}
	}
}

func TestParsesDualStackDeviceAndBlock(t *testing.T) {
	c, err := ParseUCI([]byte("config ios_location_spoofer 'main'\noption device_ipv4 '192.0.2.10'\noption device_ipv6 '2001:db8::10'\noption ipv6_block '1'\n"))
	if err != nil || c.DeviceIPv4 != "192.0.2.10" || c.DeviceIPv6 != "2001:db8::10" || !c.IPv6Block {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestDeviceListAndFamilyValidation(t *testing.T) {
	c, err := ParseUCI([]byte("config ios_location_spoofer 'main'\nlist device_macs '02:00:00:00:00:01'\nlist device_ips '192.0.2.20'\nlist device_ips '2001:db8::20'\n"))
	if err != nil || len(c.DeviceMACs) != 1 || len(c.DeviceIPs) != 2 {
		t.Fatalf("lists not parsed: %#v %v", c, err)
	}
	if _, err := ParseUCI([]byte("config ios_location_spoofer 'main'\noption device_ipv4 '2001:db8::1'\n")); err == nil {
		t.Fatal("accepted IPv6 in device_ipv4")
	}
	if _, err := ParseUCI([]byte("config ios_location_spoofer 'main'\noption device_ipv6 '192.0.2.1'\n")); err == nil {
		t.Fatal("accepted IPv4 in device_ipv6")
	}
}

func TestWLocHostAllowlist(t *testing.T) {
	for _, host := range []string{
		"gsp-ssl.ls.apple.com",
		"gspe1-ssl.ls.apple.com",
		"gs-loc.apple.com",
		"gs-loc-cn.apple.com",
		"bluedot.is.autonavi.com",
		"bluedot.is.autonavi.com.gds.alibabadns.com",
	} {
		input := "config ios_location_spoofer 'main'\nlist wloc_host '" + host + "'\n"
		if _, err := ParseUCI([]byte(input)); err != nil {
			t.Fatalf("supported WLoc host %q was rejected: %v", host, err)
		}
	}
	if _, err := ParseUCI([]byte("config ios_location_spoofer 'main'\nlist wloc_host 'example.com'\n")); err == nil {
		t.Fatal("unsupported WLoc host was accepted")
	}
}

func TestRejectsNormalizedDuplicates(t *testing.T) {
	if _, err := ParseUCI([]byte("config ios_location_spoofer 'main'\nlist device_macs '02:AA:BB:CC:DD:EE'\nlist device_macs '02:aa:bb:cc:dd:ee'\n")); err == nil {
		t.Fatal("accepted duplicate MAC with different case")
	}
	if _, err := ParseUCI([]byte("config ios_location_spoofer 'main'\nlist device_ips '2001:0db8:0:0:0:0:0:1'\nlist device_ips '2001:db8::1'\n")); err == nil {
		t.Fatal("accepted equivalent IPv6 duplicates")
	}
}

func TestParsesManagedDevices(t *testing.T) {
	input := "config ios_location_spoofer 'main'\noption enabled '1'\n" +
		"config device\noption enabled '1'\noption name 'Phone'\noption mac '02:00:00:00:00:11'\n" +
		"config device\noption enabled '0'\noption mac '02:00:00:00:00:12'\n" +
		"config profile 'home'\noption active '1'\noption latitude '22.3'\noption longitude '114.2'\noption random_radius '25'\n"
	c, err := ParseUCI([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.DeviceMACs) != 1 || c.DeviceMACs[0] != "02:00:00:00:00:11" {
		t.Fatalf("enabled device sections not selected: %#v", c.DeviceMACs)
	}
	if !c.ProfileSet || c.Profile.Latitude != 22.3 || c.Profile.Longitude != 114.2 || c.Profile.RandomRadius != 25 {
		t.Fatalf("active profile not selected: %#v", c.Profile)
	}
}

func TestSelectsOneActiveProfile(t *testing.T) {
	input := "config ios_location_spoofer 'main'\n" +
		"config profile 'one'\noption active '0'\noption latitude '1'\noption longitude '2'\n" +
		"config profile 'two'\noption active '1'\noption latitude '3'\noption longitude '4'\n"
	c, err := ParseUCI([]byte(input))
	if err != nil || !c.ProfileSet || c.Profile.Latitude != 3 || c.Profile.Longitude != 4 {
		t.Fatalf("wrong active profile: %#v %v", c.Profile, err)
	}
	duplicate := input + "config profile 'three'\noption active '1'\noption latitude '5'\noption longitude '6'\n"
	if _, err := ParseUCI([]byte(duplicate)); err == nil {
		t.Fatal("multiple active profiles were accepted")
	}
}

func TestLegacyProfileRemainsActive(t *testing.T) {
	c, err := ParseUCI([]byte("config ios_location_spoofer 'main'\nconfig profile 'location'\noption latitude '35.5'\noption longitude '139.7'\n"))
	if err != nil || !c.ProfileSet || c.Profile.Latitude != 35.5 {
		t.Fatalf("legacy profile compatibility failed: %#v %v", c.Profile, err)
	}
}
