package cert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// Paths holds the filesystem paths to all certificate and key files.
type Paths struct {
	CAKey  string
	CACert string
	Key    string
	Cert   string
}

// Info describes the current certificate state exported to the frontend.
type Info struct {
	CACertPath  string `json:"caCertPath"`
	CertPath    string `json:"certPath"`
	Exists      bool   `json:"exists"`
	IsInstalled bool   `json:"isInstalled"`
}

// InstallResult is the outcome of a certificate installation attempt.
type InstallResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Command string `json:"command"` // manual fallback command for the user
}

// certDir returns (and creates) the per-user directory where certs are stored.
func certDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "WavelogGate", "certs")
	return dir, os.MkdirAll(dir, 0o700)
}

// Setup ensures a valid Root CA and server certificate exist.
// It generates the CA if missing/expired, then issues a server cert signed by the CA.
// newlyGenerated is true when the CA was (re)created — the caller should prompt
// the user to install the CA into the system trust store.
// Returns (paths, newlyGenerated, error).
func Setup() (Paths, bool, error) {
	dir, err := certDir()
	if err != nil {
		return Paths{}, false, err
	}

	p := Paths{
		CAKey:  filepath.Join(dir, "ca.key"),
		CACert: filepath.Join(dir, "ca.crt"),
		Key:    filepath.Join(dir, "server.key"),
		Cert:   filepath.Join(dir, "server.crt"),
	}

	newlyGenerated := false

	if !caValid(p) {
		if err := generateCA(p); err != nil {
			return Paths{}, false, err
		}
		newlyGenerated = true
	}

	if !serverCertValid(p) {
		if err := generateServerCert(p); err != nil {
			return Paths{}, false, err
		}
	}

	return p, newlyGenerated, nil
}

// GetInfo returns the current certificate state.
func GetInfo(p Paths) Info {
	_, caErr := os.Stat(p.CACert)
	_, certErr := os.Stat(p.Cert)
	exists := caErr == nil && certErr == nil
	return Info{
		CACertPath:  p.CACert,
		CertPath:    p.Cert,
		Exists:      exists,
		IsInstalled: exists && IsCertInstalled(p.CACert),
	}
}

// caValid returns true when the CA cert and key exist and the cert is not expired.
func caValid(p Paths) bool {
	raw, err := os.ReadFile(p.CACert)
	if err != nil {
		return false
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}
	if time.Now().After(cert.NotAfter) {
		return false
	}
	_, err = os.Stat(p.CAKey)
	return err == nil
}

// serverCertValid returns true when the server cert/key pair is loadable, not
// expired, and was signed by the current CA.
func serverCertValid(p Paths) bool {
	tlsCert, err := tls.LoadX509KeyPair(p.Cert, p.Key)
	if err != nil {
		return false
	}
	if len(tlsCert.Certificate) == 0 {
		return false
	}
	serverCert, err := x509.ParseCertificate(tlsCert.Certificate[0])
	if err != nil {
		return false
	}
	if time.Now().After(serverCert.NotAfter) {
		return false
	}

	caRaw, err := os.ReadFile(p.CACert)
	if err != nil {
		return false
	}
	caBlock, _ := pem.Decode(caRaw)
	if caBlock == nil {
		return false
	}
	caCert, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil {
		return false
	}
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	_, err = serverCert.Verify(x509.VerifyOptions{Roots: pool})
	return err == nil
}

// generateCA creates a new ECDSA P-256 Root CA valid for 10 years.
func generateCA(p Paths) error {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}

	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "WavelogGate CA",
			Organization: []string{"WavelogGate"},
		},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return err
	}

	if err := writePEM(p.CAKey, "EC PRIVATE KEY", mustMarshalKey(priv), 0o600); err != nil {
		return err
	}
	return writePEM(p.CACert, "CERTIFICATE", certDER, 0o644)
}

// generateServerCert issues a server certificate signed by the CA.
// The cert is valid for 2 years and covers 127.0.0.1, ::1 and localhost.
func generateServerCert(p Paths) error {
	caKeyPEM, err := os.ReadFile(p.CAKey)
	if err != nil {
		return err
	}
	caKeyBlock, _ := pem.Decode(caKeyPEM)
	caKey, err := x509.ParseECPrivateKey(caKeyBlock.Bytes)
	if err != nil {
		return err
	}

	caCertPEM, err := os.ReadFile(p.CACert)
	if err != nil {
		return err
	}
	caCertBlock, _ := pem.Decode(caCertPEM)
	caCert, err := x509.ParseCertificate(caCertBlock.Bytes)
	if err != nil {
		return err
	}

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}

	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "WavelogGate Local",
			Organization: []string{"WavelogGate"},
		},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(2 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  false,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		DNSNames:              []string{"localhost"},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &priv.PublicKey, caKey)
	if err != nil {
		return err
	}

	if err := writePEM(p.Key, "EC PRIVATE KEY", mustMarshalKey(priv), 0o600); err != nil {
		return err
	}
	return writePEM(p.Cert, "CERTIFICATE", certDER, 0o644)
}

// writePEM writes a PEM-encoded block to a file with the given permissions.
func writePEM(path, blockType string, der []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer f.Close()
	return pem.Encode(f, &pem.Block{Type: blockType, Bytes: der})
}

// mustMarshalKey marshals an ECDSA private key to DER, panicking on error
// (should never happen for freshly generated keys).
func mustMarshalKey(priv *ecdsa.PrivateKey) []byte {
	der, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		panic("cert: marshal EC key: " + err.Error())
	}
	return der
}
