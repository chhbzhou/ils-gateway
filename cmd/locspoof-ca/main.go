package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/chhbzhou/ils-gateway/internal/proxy"
)

const profileSignerCommonName = "iLS Gateway"

var cmsSign = signCMSWithOpenSSL

var hosts = []string{
	"gsp-ssl.ls.apple.com",
	"gspe1-ssl.ls.apple.com",
	"gs-loc.apple.com",
	"gs-loc-cn.apple.com",
	"bluedot.is.autonavi.com",
	"bluedot.is.autonavi.com.gds.alibabadns.com",
}

func main() {
	dir := flag.String("dir", "/etc/locspoof/pki", "output directory")
	flag.Parse()
	if err := run(*dir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(dir string) error {
	if err := os.MkdirAll(dir, 0750); err != nil {
		return err
	}
	// The daemon needs to traverse the directory to read public artifacts and
	// leaf bundles, while both private signing keys remain root-only.
	if err := os.Chmod(dir, 0750); err != nil {
		return err
	}
	ca, caKey, err := loadCA(dir)
	if err != nil {
		certExists := fileExists(filepath.Join(dir, "ca-cert.pem"))
		keyExists := fileExists(filepath.Join(dir, "ca-key.pem"))
		if certExists || keyExists {
			return fmt.Errorf("existing CA is damaged or mismatched: %w", err)
		}
		caKey, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return err
		}
		serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
		ca = &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "iLS Gateway Root"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().AddDate(10, 0, 0), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
		der, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
		if err != nil {
			return err
		}
		if err := atomicWrite(filepath.Join(dir, "ca-cert.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0640); err != nil {
			return err
		}
		keyDER, _ := x509.MarshalECPrivateKey(caKey)
		if err := atomicWrite(filepath.Join(dir, "ca-key.pem"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0600); err != nil {
			return err
		}
	}
	if err := os.Chmod(filepath.Join(dir, "ca-key.pem"), 0600); err != nil {
		return err
	}
	signerCertPath := filepath.Join(dir, "profile-signing-cert.pem")
	signerKeyPath := filepath.Join(dir, "profile-signing-key.pem")
	if err := ensureProfileSigner(signerCertPath, signerKeyPath, ca, caKey); err != nil {
		return err
	}
	signedProfile, err := cmsSign(
		[]byte(proxy.EnrollmentMobileConfig(ca)),
		signerCertPath,
		signerKeyPath,
	)
	if err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(dir, "ca.mobileconfig"), signedProfile, 0640); err != nil {
		return err
	}
	for _, host := range hosts {
		if err := ensureLeaf(filepath.Join(dir, host+".pem"), host, ca, caKey); err != nil {
			return err
		}
	}
	if err := os.Chmod(filepath.Join(dir, "ca-cert.pem"), 0640); err != nil {
		return err
	}
	if err := os.Chmod(signerCertPath, 0640); err != nil {
		return err
	}
	if err := os.Chmod(signerKeyPath, 0600); err != nil {
		return err
	}
	if err := os.Chmod(filepath.Join(dir, "ca.mobileconfig"), 0640); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0750); err != nil {
		return err
	}
	return nil
}

func ensureLeaf(path, host string, ca *x509.Certificate, caKey *ecdsa.PrivateKey) error {
	if leafValid(path, host, ca) {
		return os.Chmod(path, 0640)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	now := time.Now()
	notAfter := now.AddDate(2, 0, 0)
	if ca.NotAfter.Before(notAfter) {
		notAfter = ca.NotAfter
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leaf, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		return err
	}
	leafKey, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	bundle := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf})
	bundle = append(bundle, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: leafKey})...)
	return atomicWrite(path, bundle, 0640)
}

func leafValid(path, host string, ca *x509.Certificate) bool {
	pair, err := tls.LoadX509KeyPair(path, path)
	if err != nil || len(pair.Certificate) == 0 {
		return false
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return false
	}
	now := time.Now()
	return !cert.NotBefore.After(now) && cert.NotAfter.After(now) &&
		cert.KeyUsage&x509.KeyUsageDigitalSignature != 0 &&
		hasExtKeyUsage(cert.ExtKeyUsage, x509.ExtKeyUsageServerAuth) &&
		cert.VerifyHostname(host) == nil && cert.CheckSignatureFrom(ca) == nil
}

func hasExtKeyUsage(usages []x509.ExtKeyUsage, target x509.ExtKeyUsage) bool {
	for _, usage := range usages {
		if usage == target {
			return true
		}
	}
	return false
}

func ensureProfileSigner(certPath, keyPath string, ca *x509.Certificate, caKey *ecdsa.PrivateKey) error {
	if profileSignerValid(certPath, keyPath, ca) {
		return nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	now := time.Now()
	notAfter := now.AddDate(5, 0, 0)
	if ca.NotAfter.Before(notAfter) {
		notAfter = ca.NotAfter
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: profileSignerCommonName, Organization: []string{"iLS Gateway"}},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              notAfter,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning, x509.ExtKeyUsageEmailProtection},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	if err := atomicWrite(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0640); err != nil {
		return err
	}
	return atomicWrite(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0600)
}

func profileSignerValid(certPath, keyPath string, ca *x509.Certificate) bool {
	certPEM, certErr := os.ReadFile(certPath)
	keyPEM, keyErr := os.ReadFile(keyPath)
	if certErr != nil || keyErr != nil {
		return false
	}
	certBlock, _ := pem.Decode(certPEM)
	keyBlock, _ := pem.Decode(keyPEM)
	if certBlock == nil || keyBlock == nil {
		return false
	}
	cert, certErr := x509.ParseCertificate(certBlock.Bytes)
	key, keyErr := x509.ParseECPrivateKey(keyBlock.Bytes)
	now := time.Now()
	if certErr != nil || keyErr != nil ||
		cert.NotBefore.After(now) || cert.NotAfter.Before(now) ||
		cert.KeyUsage&x509.KeyUsageDigitalSignature == 0 || cert.CheckSignatureFrom(ca) != nil {
		return false
	}
	certPub, certErr := x509.MarshalPKIXPublicKey(cert.PublicKey)
	keyPub, keyErr := x509.MarshalPKIXPublicKey(&key.PublicKey)
	return certErr == nil && keyErr == nil && bytes.Equal(certPub, keyPub)
}

func signCMSWithOpenSSL(profile []byte, signerCertPath, signerKeyPath string) ([]byte, error) {
	cmd := exec.Command("openssl", "cms", "-sign", "-binary", "-nodetach", "-outform", "DER", "-md", "sha256", "-nosmimecap", "-signer", signerCertPath, "-inkey", signerKeyPath)
	cmd.Stdin = bytes.NewReader(profile)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("sign configuration profile: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if stdout.Len() == 0 {
		return nil, fmt.Errorf("sign configuration profile: openssl returned an empty document")
	}
	return stdout.Bytes(), nil
}

func fileExists(path string) bool { _, err := os.Stat(path); return err == nil }

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err = tmp.Chmod(mode); err == nil {
		_, err = tmp.Write(data)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	if dirFile, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dirFile.Sync()
		_ = dirFile.Close()
	}
	return nil
}

func loadCA(dir string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	certPEM, err := os.ReadFile(filepath.Join(dir, "ca-cert.pem"))
	if err != nil {
		return nil, nil, err
	}
	keyPEM, err := os.ReadFile(filepath.Join(dir, "ca-key.pem"))
	if err != nil {
		return nil, nil, err
	}
	cb, _ := pem.Decode(certPEM)
	kb, _ := pem.Decode(keyPEM)
	if cb == nil || kb == nil {
		return nil, nil, fmt.Errorf("invalid CA PEM")
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	if !cert.IsCA || !cert.BasicConstraintsValid || cert.KeyUsage&x509.KeyUsageCertSign == 0 || cert.NotBefore.After(now) || cert.NotAfter.Before(now) || cert.CheckSignatureFrom(cert) != nil {
		return nil, nil, fmt.Errorf("invalid or expired CA certificate")
	}
	key, err := x509.ParseECPrivateKey(kb.Bytes)
	if err != nil {
		return nil, nil, err
	}
	certPub, err := x509.MarshalPKIXPublicKey(cert.PublicKey)
	if err != nil {
		return nil, nil, err
	}
	keyPub, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, nil, err
	}
	if string(certPub) != string(keyPub) {
		return nil, nil, fmt.Errorf("CA certificate and key do not match")
	}
	return cert, key, nil
}
