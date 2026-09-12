package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	original := cmsSign
	cmsSign = func(profile []byte, _, _ string) ([]byte, error) {
		return append([]byte("test-cms:"), profile...), nil
	}
	code := m.Run()
	cmsSign = original
	os.Exit(code)
}

func TestCARunPersistsAndRejectsDamage(t *testing.T) {
	d := t.TempDir()
	if err := run(d); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(filepath.Join(d, "ca-cert.pem"))
	if err != nil {
		t.Fatal(err)
	}
	firstSigner, err := os.ReadFile(filepath.Join(d, "profile-signing-cert.pem"))
	if err != nil {
		t.Fatal(err)
	}
	firstLeaf, err := os.ReadFile(filepath.Join(d, hosts[0]+".pem"))
	if err != nil {
		t.Fatal(err)
	}
	if err := run(d); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(filepath.Join(d, "ca-cert.pem"))
	if string(first) != string(second) {
		t.Fatal("CA rotated unexpectedly")
	}
	secondSigner, _ := os.ReadFile(filepath.Join(d, "profile-signing-cert.pem"))
	if string(firstSigner) != string(secondSigner) {
		t.Fatal("profile signing identity rotated unexpectedly")
	}
	secondLeaf, _ := os.ReadFile(filepath.Join(d, hosts[0]+".pem"))
	if string(firstLeaf) != string(secondLeaf) {
		t.Fatal("valid leaf certificate rotated unexpectedly")
	}
	leaf := readCertificate(t, filepath.Join(d, hosts[0]+".pem"))
	if !hasExtKeyUsage(leaf.ExtKeyUsage, x509.ExtKeyUsageServerAuth) {
		t.Fatal("leaf does not permit TLS server authentication")
	}
	signer := readCertificate(t, filepath.Join(d, "profile-signing-cert.pem"))
	if signer.Subject.CommonName != profileSignerCommonName {
		t.Fatalf("signer common name = %q", signer.Subject.CommonName)
	}
	if signer.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		t.Fatal("signer does not permit digital signatures")
	}
	ca := readCertificate(t, filepath.Join(d, "ca-cert.pem"))
	if err := signer.CheckSignatureFrom(ca); err != nil {
		t.Fatalf("signer was not issued by the persistent CA: %v", err)
	}
	profile, err := os.ReadFile(filepath.Join(d, "ca.mobileconfig"))
	if err != nil || !strings.HasPrefix(string(profile), "test-cms:") {
		t.Fatalf("signed profile was not generated: %v", err)
	}
	if err := os.WriteFile(filepath.Join(d, "ca-cert.pem"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run(d); err == nil {
		t.Fatal("damaged CA silently replaced")
	}
}

func TestCARunAppliesDaemonReadablePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose Unix mode/UID semantics; run tools/test-pki-permissions.sh on Linux")
	}
	d := t.TempDir()
	if err := run(d); err != nil {
		t.Fatal(err)
	}
	assertMode := func(name string, want os.FileMode) {
		t.Helper()
		info, err := os.Stat(filepath.Join(d, name))
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s mode = %04o, want %04o", name, got, want)
		}
	}
	assertMode(".", 0750)
	assertMode("ca-cert.pem", 0640)
	assertMode("ca-key.pem", 0600)
	assertMode("profile-signing-cert.pem", 0640)
	assertMode("profile-signing-key.pem", 0600)
	assertMode("ca.mobileconfig", 0640)
	for _, host := range hosts {
		assertMode(host+".pem", 0640)
	}
}

func TestCARunRepairsProfileSignerWithoutRotatingCA(t *testing.T) {
	d := t.TempDir()
	if err := run(d); err != nil {
		t.Fatal(err)
	}
	caBefore, _ := os.ReadFile(filepath.Join(d, "ca-cert.pem"))
	if err := os.WriteFile(filepath.Join(d, "profile-signing-cert.pem"), []byte("broken"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := run(d); err != nil {
		t.Fatal(err)
	}
	caAfter, _ := os.ReadFile(filepath.Join(d, "ca-cert.pem"))
	if string(caBefore) != string(caAfter) {
		t.Fatal("repairing profile signer rotated CA")
	}
	signer := readCertificate(t, filepath.Join(d, "profile-signing-cert.pem"))
	ca := readCertificate(t, filepath.Join(d, "ca-cert.pem"))
	if signer.Subject.CommonName != profileSignerCommonName || signer.CheckSignatureFrom(ca) != nil {
		t.Fatal("profile signer was not repaired correctly")
	}
}

func TestCARunPreservesExistingCustomProfileSigner(t *testing.T) {
	d := t.TempDir()
	if err := run(d); err != nil {
		t.Fatal(err)
	}
	writeProfileSigner(t, d, "Existing Custom Signer", "Existing Installation")
	certPath := filepath.Join(d, "profile-signing-cert.pem")
	keyPath := filepath.Join(d, "profile-signing-key.pem")
	certBefore, _ := os.ReadFile(certPath)
	keyBefore, _ := os.ReadFile(keyPath)

	if err := run(d); err != nil {
		t.Fatal(err)
	}
	certAfter, _ := os.ReadFile(certPath)
	keyAfter, _ := os.ReadFile(keyPath)
	if string(certBefore) != string(certAfter) || string(keyBefore) != string(keyAfter) {
		t.Fatal("existing profile signing identity rotated unexpectedly")
	}
}

func TestOpenSSLCMSSigning(t *testing.T) {
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl is unavailable")
	}
	d := t.TempDir()
	if err := run(d); err != nil {
		t.Fatal(err)
	}
	unsigned := []byte("<?xml version=\"1.0\"?><plist><dict/></plist>")
	signed, err := signCMSWithOpenSSL(unsigned,
		filepath.Join(d, "profile-signing-cert.pem"),
		filepath.Join(d, "profile-signing-key.pem"),
	)
	if err != nil {
		t.Fatal(err)
	}
	signedPath := filepath.Join(d, "signed.mobileconfig")
	verifiedPath := filepath.Join(d, "verified.plist")
	mustWrite(t, signedPath, signed, 0600)
	cmd := exec.Command("openssl", "cms", "-verify", "-binary", "-inform", "DER", "-in", signedPath, "-CAfile", filepath.Join(d, "ca-cert.pem"), "-purpose", "any", "-out", verifiedPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("verify signed profile: %v: %s", err, output)
	}
	verified, err := os.ReadFile(verifiedPath)
	if err != nil || string(verified) != string(unsigned) {
		t.Fatalf("verified content mismatch: %v", err)
	}
	certificatesPath := filepath.Join(d, "embedded-certificates.pem")
	cmd = exec.Command("openssl", "pkcs7", "-inform", "DER", "-in", signedPath, "-print_certs", "-out", certificatesPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("extract embedded certificates: %v: %s", err, output)
	}
	certificates, err := os.ReadFile(certificatesPath)
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(string(certificates), "-----BEGIN CERTIFICATE-----"); count != 1 {
		t.Fatalf("CMS must embed only the profile signer, got %d certificates", count)
	}
}

func TestCARunRejectsPartialAndMismatchedCA(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, dir string)
	}{
		{name: "missing key", mutate: func(t *testing.T, dir string) { _ = os.Remove(filepath.Join(dir, "ca-key.pem")) }},
		{name: "missing cert", mutate: func(t *testing.T, dir string) { _ = os.Remove(filepath.Join(dir, "ca-cert.pem")) }},
		{name: "bad pem", mutate: func(t *testing.T, dir string) {
			mustWrite(t, filepath.Join(dir, "ca-cert.pem"), []byte("not pem"), 0600)
		}},
		{name: "expired self signed", mutate: expireCA},
		{name: "mismatched key", mutate: mismatchCAKey},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := t.TempDir()
			if err := run(d); err != nil {
				t.Fatal(err)
			}
			tc.mutate(t, d)
			mutated, readErr := os.ReadFile(filepath.Join(d, "ca-cert.pem"))
			if err := run(d); err == nil {
				t.Fatal("damaged CA accepted")
			}
			after, afterErr := os.ReadFile(filepath.Join(d, "ca-cert.pem"))
			if readErr != nil {
				if !os.IsNotExist(readErr) || !os.IsNotExist(afterErr) {
					t.Fatal("missing CA file was recreated")
				}
			} else if afterErr != nil || string(after) != string(mutated) {
				t.Fatal("CA file changed after damage")
			}
		})
	}
}

func TestCARunRepairsLeavesWithoutRotatingCA(t *testing.T) {
	d := t.TempDir()
	if err := run(d); err != nil {
		t.Fatal(err)
	}
	certPath := filepath.Join(d, "ca-cert.pem")
	before, _ := os.ReadFile(certPath)
	leaf := filepath.Join(d, hosts[0]+".pem")
	if err := os.Remove(leaf); err != nil {
		t.Fatal(err)
	}
	if err := run(d); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(leaf); err != nil {
		t.Fatalf("missing leaf was not repaired: %v", err)
	}
	if err := os.WriteFile(leaf, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run(d); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(certPath)
	if string(after) != string(before) {
		t.Fatal("leaf repair rotated CA")
	}
	if _, err := tls.LoadX509KeyPair(leaf, leaf); err != nil {
		t.Fatalf("repaired leaf invalid: %v", err)
	}
}

func TestCARunRepairsLeafForWrongHost(t *testing.T) {
	d := t.TempDir()
	if err := run(d); err != nil {
		t.Fatal(err)
	}
	wrong, err := os.ReadFile(filepath.Join(d, hosts[0]+".pem"))
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(d, hosts[1]+".pem")
	mustWrite(t, target, wrong, 0640)
	if err := run(d); err != nil {
		t.Fatal(err)
	}
	if err := readCertificate(t, target).VerifyHostname(hosts[1]); err != nil {
		t.Fatalf("wrong-host leaf was not repaired: %v", err)
	}
}

func TestCARunRepairsLeafWithoutServerAuth(t *testing.T) {
	d := t.TempDir()
	if err := run(d); err != nil {
		t.Fatal(err)
	}
	ca := readCertificate(t, filepath.Join(d, "ca-cert.pem"))
	caKey := readCAKey(t, d)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(100),
		Subject:      pkix.Name{CommonName: hosts[0]},
		DNSNames:     []string{hosts[0]},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.AddDate(2, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	bundle := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	bundle = append(bundle, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})...)
	target := filepath.Join(d, hosts[0]+".pem")
	mustWrite(t, target, bundle, 0640)
	if err := run(d); err != nil {
		t.Fatal(err)
	}
	leaf := readCertificate(t, target)
	if !hasExtKeyUsage(leaf.ExtKeyUsage, x509.ExtKeyUsageServerAuth) {
		t.Fatal("leaf without serverAuth was not repaired")
	}
}

func mustWrite(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}

func readCertificate(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(b)
	if block == nil {
		t.Fatalf("invalid certificate PEM: %s", path)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func readCAKey(t *testing.T, dir string) *ecdsa.PrivateKey {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "ca-key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	bl, _ := pem.Decode(b)
	if bl == nil {
		t.Fatal("missing key PEM")
	}
	k, err := x509.ParseECPrivateKey(bl.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func writeProfileSigner(t *testing.T, dir, commonName, organization string) {
	t.Helper()
	ca := readCertificate(t, filepath.Join(dir, "ca-cert.pem"))
	caKey := readCAKey(t, dir)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(101),
		Subject:      pkix.Name{CommonName: commonName, Organization: []string{organization}},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.AddDate(5, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning, x509.ExtKeyUsageEmailProtection},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "profile-signing-cert.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0640)
	mustWrite(t, filepath.Join(dir, "profile-signing-key.pem"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0600)
}

func expireCA(t *testing.T, dir string) {
	t.Helper()
	k := readCAKey(t, dir)
	now := time.Now().Add(-time.Hour)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(99), Subject: pkix.Name{CommonName: "expired"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(-time.Minute), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "ca-cert.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600)
}

func mismatchCAKey(t *testing.T, dir string) {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalECPrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "ca-key.pem"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0600)
}
