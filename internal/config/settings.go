// Package config reads the bounded, strict settings document used at startup.
package config

import (
	"bytes"
	"errors"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
	"github.com/jpconstantineau/GoDav-Prototype/internal/winpath"

	"go.yaml.in/yaml/v3"
)

// Settings contains validated syntax and absolute data and TLS paths. Loading
// settings does not open the database or require TLS assets to exist.
type Settings struct {
	ConfigVersion        int64
	ListenAddress        string
	PublicOrigin         string
	DAVPrefix            string
	DatabasePath         string
	CertFile             string
	KeyFile              string
	AuthenticationMethod string
	MaxFileBytes         int64
	ShutdownGrace        time.Duration
}

var errSettings = errors.New("invalid or inaccessible settings")

// ValidPathSyntax checks raw operator spelling before Abs/Clean can erase an
// unsafe component or turn drive/root-relative input into an absolute path.
func ValidPathSyntax(path string) bool { return safePath(path) }

// Load reads exactly one document, returning only fixed, nonsecret errors.
func Load(filename string) (Settings, error) {
	var result Settings
	if !safePath(filename) {
		return result, errSettings
	}
	absolute, err := filepath.Abs(filename)
	if err != nil {
		return result, errSettings
	}
	f, err := os.Open(absolute)
	if err != nil {
		return result, errSettings
	}
	data, err := io.ReadAll(io.LimitReader(f, 65537))
	closeErr := f.Close()
	if err != nil || closeErr != nil || len(data) > 65536 {
		return result, errSettings
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var doc, extra yaml.Node
	if decoder.Decode(&doc) != nil || len(doc.Content) != 1 || decoder.Decode(&extra) != io.EOF {
		return result, errSettings
	}
	if !plainNodes(&doc) {
		return result, errSettings
	}
	root, ok := mapping(doc.Content[0], "config_version", "listen_address", "public_origin", "dav_prefix", "database_path", "tls", "authentication", "limits")
	if !ok {
		return result, errSettings
	}
	tls, ok := mapping(root["tls"], "cert_file", "key_file")
	if !ok {
		return result, errSettings
	}
	auth, ok := mapping(root["authentication"], "method")
	if !ok {
		return result, errSettings
	}
	limits, ok := mapping(root["limits"], "max_file_bytes")
	if !ok {
		limits, ok = mapping(root["limits"], "max_file_bytes", "shutdown_grace_ms")
	}
	if !ok {
		return result, errSettings
	}
	version, ok := positiveInt(root["config_version"])
	if !ok || version != 1 {
		return result, errSettings
	}
	maxBytes, ok := positiveInt(limits["max_file_bytes"])
	if !ok || maxBytes > files.AbsoluteMaxFileBytes {
		return result, errSettings
	}
	graceMS := int64(5000)
	if node := limits["shutdown_grace_ms"]; node != nil {
		graceMS, ok = positiveInt(node)
		if !ok || graceMS > 30000 {
			return result, errSettings
		}
	}
	for _, node := range []*yaml.Node{root["listen_address"], root["public_origin"], root["dav_prefix"], root["database_path"], tls["cert_file"], tls["key_file"], auth["method"]} {
		if node.Kind != yaml.ScalarNode || node.Tag != "!!str" || node.Value == "" {
			return result, errSettings
		}
	}
	result = Settings{
		ConfigVersion: version, ListenAddress: root["listen_address"].Value,
		PublicOrigin: root["public_origin"].Value, DAVPrefix: root["dav_prefix"].Value,
		AuthenticationMethod: auth["method"].Value, MaxFileBytes: maxBytes, ShutdownGrace: time.Duration(graceMS) * time.Millisecond,
	}
	if result.AuthenticationMethod != "basic" || !validEndpoints(result) || !validPrefix(result.DAVPrefix) {
		return Settings{}, errSettings
	}
	paths := []string{absolute}
	for _, node := range []*yaml.Node{root["database_path"], tls["cert_file"], tls["key_file"]} {
		if !safePath(node.Value) {
			return Settings{}, errSettings
		}
		p := node.Value
		if !filepath.IsAbs(p) {
			p = filepath.Join(filepath.Dir(absolute), p)
		}
		p, err = filepath.Abs(p)
		if err != nil {
			return Settings{}, errSettings
		}
		paths = append(paths, p)
	}
	result.DatabasePath, result.CertFile, result.KeyFile = paths[1], paths[2], paths[3]
	for _, suffix := range []string{"-wal", "-shm", "-journal", ".lock"} {
		paths = append(paths, result.DatabasePath+suffix)
	}
	for i, p := range paths {
		for _, other := range paths[:i] {
			if strings.EqualFold(p, other) {
				return Settings{}, errSettings
			}
		}
	}
	return result, nil
}

func plainNodes(node *yaml.Node) bool {
	if node.Anchor != "" || node.Alias != nil || node.Kind == yaml.AliasNode {
		return false
	}
	switch node.Kind {
	case yaml.DocumentNode:
	case yaml.MappingNode:
		if node.Tag != "!!map" {
			return false
		}
	case yaml.ScalarNode:
		if node.Tag != "!!str" && node.Tag != "!!int" {
			return false
		}
	default:
		return false
	}
	for _, child := range node.Content {
		if !plainNodes(child) {
			return false
		}
	}
	return true
}

func mapping(node *yaml.Node, keys ...string) (map[string]*yaml.Node, bool) {
	if node == nil || node.Kind != yaml.MappingNode || len(node.Content) != 2*len(keys) {
		return nil, false
	}
	values := make(map[string]*yaml.Node, len(keys))
	for i := 0; i < len(node.Content); i += 2 {
		key := node.Content[i]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || values[key.Value] != nil {
			return nil, false
		}
		known := false
		for _, allowed := range keys {
			if key.Value == allowed {
				known = true
				break
			}
		}
		if !known {
			return nil, false
		}
		values[key.Value] = node.Content[i+1]
	}
	return values, true
}

func positiveInt(node *yaml.Node) (int64, bool) {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!int" || node.Value == "" {
		return 0, false
	}
	for _, c := range node.Value {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(node.Value, 10, 64)
	return n, err == nil && n > 0
}

func validEndpoints(s Settings) bool {
	host, port, err := net.SplitHostPort(s.ListenAddress)
	if err != nil {
		return false
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || ip.Zone() != "" || !ip.IsLoopback() {
		return false
	}
	bindPort, ok := portNumber(port)
	if !ok {
		return false
	}
	u, err := url.Parse(s.PublicOrigin)
	if err != nil || u.Scheme != "https" || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(s.PublicOrigin, "#") || u.RawPath != "" || (u.Path != "" && u.Path != "/") {
		return false
	}
	if strings.ContainsAny(u.Host, "%\\") {
		return false
	}
	originHost := u.Hostname()
	if !strings.EqualFold(originHost, "localhost") {
		ip, err := netip.ParseAddr(originHost)
		if err != nil || ip.Zone() != "" || !ip.IsLoopback() {
			return false
		}
	}
	originPort := 443
	if u.Port() != "" {
		var ok bool
		originPort, ok = portNumber(u.Port())
		if !ok {
			return false
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return false
	}
	return bindPort == originPort
}

func portNumber(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	return n, err == nil && n > 0 && n <= 65535
}

func validPrefix(s string) bool {
	if !strings.HasPrefix(s, "/") || !strings.HasSuffix(s, "/") || strings.ContainsAny(s, "%?#\\") {
		return false
	}
	for _, c := range s {
		if c <= 32 || c == 127 {
			return false
		}
	}
	return s == "/" || path.Clean(s)+"/" == s
}

// Reject Windows lexical aliases before filepath.Clean can hide them. Filesystem
// identity, reparse points and permissions are checked by the platform layer.
func safePath(s string) bool {
	if s == "" || strings.TrimSpace(s) != s {
		return false
	}
	p := strings.ReplaceAll(s, "/", "\\")
	if strings.HasPrefix(p, "\\") {
		return false
	}
	if len(p) >= 2 && p[1] == ':' {
		if !((p[0] >= 'a' && p[0] <= 'z') || (p[0] >= 'A' && p[0] <= 'Z')) || len(p) < 3 || p[2] != '\\' {
			return false
		}
		p = p[3:]
	}
	parts := strings.Split(p, "\\")
	for i, part := range parts {
		if part == "." || part == ".." {
			if i == len(parts)-1 {
				return false
			}
			continue
		}
		if !winpath.ValidComponent(part) {
			return false
		}
	}
	return true
}
