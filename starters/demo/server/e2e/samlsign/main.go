// Command samlsign signs one SAML response for the end-to-end tier's local
// identity provider. It reads the unsigned document on standard input and writes
// the base64 transport form to standard output.
package main

import (
	"bytes"
	"crypto"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/beevik/etree"
	dsig "github.com/russellhaering/goxmldsig"
)

const (
	keyFile  = "key.pem"
	certFile = "cert.pem"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "samlsign: %v\n", err)
		os.Exit(1)
	}
}

// run parses the flags, reads the unsigned response from in and writes the
// signed, base64 encoded response to out.
func run(args []string, in io.Reader, out io.Writer) error {
	flags := flag.NewFlagSet("samlsign", flag.ContinueOnError)
	dir := flags.String("dir", "", "directory holding the identity provider's key.pem and cert.pem")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *dir == "" {
		return errors.New("-dir is required")
	}
	document, err := io.ReadAll(in)
	if err != nil {
		return fmt.Errorf("reading the response: %w", err)
	}
	encoded, err := sign(*dir, document)
	if err != nil {
		return err
	}
	_, err = io.WriteString(out, encoded)
	return err
}

// sign signs the root element of the document and returns the base64 form of the
// signed document.
func sign(dir string, document []byte) (string, error) {
	material, err := os.OpenRoot(dir)
	if err != nil {
		return "", fmt.Errorf("opening the identity provider's directory: %w", err)
	}
	defer func() { _ = material.Close() }()
	signer, err := loadSigner(material)
	if err != nil {
		return "", err
	}
	certDER, err := loadCertificate(material)
	if err != nil {
		return "", err
	}
	context, err := dsig.NewSigningContext(signer, [][]byte{certDER})
	if err != nil {
		return "", fmt.Errorf("building the signing context: %w", err)
	}
	parsed := etree.NewDocument()
	if err = parsed.ReadFromBytes(document); err != nil {
		return "", fmt.Errorf("parsing the response: %w", err)
	}
	root := parsed.Root()
	if root == nil {
		return "", errors.New("the response has no root element")
	}
	signed, err := context.SignEnveloped(root)
	if err != nil {
		return "", fmt.Errorf("signing the response: %w", err)
	}
	output := etree.NewDocument()
	output.SetRoot(signed)
	output.WriteSettings = etree.WriteSettings{CanonicalAttrVal: true, CanonicalEndTags: true, CanonicalText: true}
	var text bytes.Buffer
	if _, err := output.WriteTo(&text); err != nil {
		return "", fmt.Errorf("writing the signed response: %w", err)
	}
	return base64.StdEncoding.EncodeToString(text.Bytes()), nil
}

func loadSigner(material *os.Root) (crypto.Signer, error) {
	block, err := readPEM(material, keyFile)
	if err != nil {
		return nil, err
	}
	if key, parseErr := x509.ParsePKCS8PrivateKey(block.Bytes); parseErr == nil {
		signer, ok := key.(crypto.Signer)
		if !ok {
			return nil, fmt.Errorf("the key in %s cannot sign", keyFile)
		}
		return signer, nil
	}
	if key, parseErr := x509.ParsePKCS1PrivateKey(block.Bytes); parseErr == nil {
		return key, nil
	}
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("the key in %s is not a PKCS #8, PKCS #1 or SEC 1 private key", keyFile)
	}
	return key, nil
}

func loadCertificate(material *os.Root) ([]byte, error) {
	block, err := readPEM(material, certFile)
	if err != nil {
		return nil, err
	}
	if _, err := x509.ParseCertificate(block.Bytes); err != nil {
		return nil, fmt.Errorf("the certificate in %s does not parse: %w", certFile, err)
	}
	return block.Bytes, nil
}

func readPEM(material *os.Root, path string) (*pem.Block, error) {
	raw, err := material.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("%s holds no PEM block", path)
	}
	return block, nil
}
