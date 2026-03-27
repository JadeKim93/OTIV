package vpn

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

type PKI struct {
	dir    string
	caCert *x509.Certificate
	caKey  *ecdsa.PrivateKey
}

func NewPKI(dir string) (*PKI, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}

	pki := &PKI{dir: dir}
	caKeyPath := filepath.Join(dir, "ca.key")

	if _, err := os.Stat(caKeyPath); os.IsNotExist(err) {
		return pki, pki.generateCA()
	}
	return pki, pki.loadCA()
}

func (p *PKI) generateCA() error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "OTIV CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return err
	}
	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return err
	}

	p.caCert = cert
	p.caKey = key

	if err := writeECKey(filepath.Join(p.dir, "ca.key"), key); err != nil {
		return err
	}
	return writeCertPEM(filepath.Join(p.dir, "ca.crt"), certDER)
}

func (p *PKI) loadCA() error {
	keyPEM, err := os.ReadFile(filepath.Join(p.dir, "ca.key"))
	if err != nil {
		return err
	}
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return fmt.Errorf("failed to decode CA key PEM")
	}
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return err
	}

	certPEM, err := os.ReadFile(filepath.Join(p.dir, "ca.crt"))
	if err != nil {
		return err
	}
	block, _ = pem.Decode(certPEM)
	if block == nil {
		return fmt.Errorf("failed to decode CA cert PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return err
	}

	p.caCert = cert
	p.caKey = key
	return nil
}

type CertKeyPair struct {
	CertPEM []byte
	KeyPEM  []byte
}

func (p *PKI) GenerateServerCert(cn string) (*CertKeyPair, error) {
	return p.generateCert(cn, x509.ExtKeyUsageServerAuth)
}

func (p *PKI) GenerateClientCert(cn string) (*CertKeyPair, error) {
	return p.generateCert(cn, x509.ExtKeyUsageClientAuth)
}

func (p *PKI) CACertPEM() ([]byte, error) {
	return os.ReadFile(filepath.Join(p.dir, "ca.crt"))
}

func (p *PKI) generateCert(cn string, usage x509.ExtKeyUsage) (*CertKeyPair, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{usage},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, p.caCert, &key.PublicKey, p.caKey)
	if err != nil {
		return nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}

	return &CertKeyPair{
		CertPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}),
		KeyPEM:  pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
	}, nil
}

func writeECKey(path string, key *ecdsa.PrivateKey) error {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	return os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0600)
}

func writeCertPEM(path string, der []byte) error {
	return os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0644)
}
