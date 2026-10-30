package push

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/paas-ops/kubectl-triage/internal/bundle"
	"github.com/paas-ops/kubectl-triage/internal/redact"
	"github.com/paas-ops/kubectl-triage/internal/report"
	"github.com/paas-ops/kubectl-triage/pkg/schema"
)

// CLIVersion is the audit snapshot embedded in the push payload. It is copied
// from the local report versions (cli/rules/model).
type CLIVersion struct {
	Version string `json:"version"`
	Rules   string `json:"rules"`
	Model   string `json:"model"`
}

// EvidenceRef is the hash-only evidence reference uploaded by default. The
// evidence body stays local unless --include-evidence is set.
type EvidenceRef struct {
	ID         string `json:"id"`
	Source     string `json:"source"`
	ContentRef string `json:"content_ref"`
	SHA256     string `json:"sha256"`
}

// BundleRef describes the optional gzipped evidence bundle. Included=false is
// the default and must not carry content fields.
type BundleRef struct {
	Included      bool   `json:"included"`
	ContentType   string `json:"content_type,omitempty"`
	SHA256        string `json:"sha256,omitempty"`
	SizeBytes     int64  `json:"size_bytes,omitempty"`
	ContentBase64 string `json:"content_base64,omitempty"`
}

// Payload is the frozen protocol_version 1.0 request body (ADR-006 D2).
type Payload struct {
	ProtocolVersion string          `json:"protocol_version"`
	PushID          string          `json:"push_id"`
	CreatedAt       string          `json:"created_at"`
	CLI             CLIVersion      `json:"cli"`
	Report          json.RawMessage `json:"report"`
	ReportSHA256    string          `json:"report_sha256"`
	Evidence        []EvidenceRef   `json:"evidence"`
	Bundle          BundleRef       `json:"bundle"`
}

// JSON renders the payload for --dry-run --out audit files. It never contains
// the enterprise token.
func (p *Payload) JSON() ([]byte, error) {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode push payload: %w", err)
	}
	return append(data, byte(10)), nil
}

// Build validates the local bundle and report, applies the pre-push security
// checks, and assembles the deterministic protocol payload. It never touches
// the network.
func Build(dir string, cfg Config) (*Payload, *Result, error) {
	if err := cfg.Validate(); err != nil {
		return nil, nil, err
	}

	b, err := bundle.Load(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("读取事故包失败：%w", err)
	}
	// Push always re-checks the bundle in strict mode: the enterprise bridge
	// must never upload credentials even if analyze was run with --redact off.
	b.RedactMode = redact.ModeStrict
	if err := b.Validate(); err != nil {
		var security *bundle.SecurityError
		if errors.As(err, &security) {
			return nil, nil, &SecurityError{Reason: "事故包包含疑似凭据/密钥内容，已拒绝推送；请先脱敏后重试"}
		}
		return nil, nil, fmt.Errorf("事故包校验失败：%w", err)
	}

	reportPath := filepath.Join(dir, "report.json")
	raw, err := os.ReadFile(reportPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, fmt.Errorf("未找到 %s，请先运行 kubectl-triage analyze 生成报告", reportPath)
		}
		return nil, nil, fmt.Errorf("读取报告失败：%w", err)
	}
	if err := schema.ValidateReport(raw); err != nil {
		return nil, nil, fmt.Errorf("report.json 不合法：%w", err)
	}

	// A report that contains credential-like text must never leave the machine,
	// even if it was produced with a weaker redaction mode.
	if scrubbed := redact.Scrub(raw); !bytes.Equal(scrubbed, raw) {
		return nil, nil, &SecurityError{Reason: "报告包含疑似凭据/密钥内容，已拒绝推送；请使用 --redact strict 重新生成报告"}
	}

	var rep report.Report
	if err := json.Unmarshal(raw, &rep); err != nil {
		return nil, nil, fmt.Errorf("解析 report.json 失败：%w", err)
	}
	canonical, err := json.Marshal(&rep)
	if err != nil {
		return nil, nil, fmt.Errorf("规范化 report.json 失败：%w", err)
	}
	if err := schema.ValidateReport(canonical); err != nil {
		return nil, nil, fmt.Errorf("规范化后的 report.json 不合法：%w", err)
	}

	pushID := strings.TrimSpace(cfg.PushID)
	if pushID == "" {
		id, err := newUUIDv4()
		if err != nil {
			return nil, nil, fmt.Errorf("生成 push_id 失败：%w", err)
		}
		pushID = id
	}
	now := cfg.now()
	payload := &Payload{
		ProtocolVersion: ProtocolVersion,
		PushID:          pushID,
		CreatedAt:       now.UTC().Format(time.RFC3339),
		CLI: CLIVersion{
			Version: versionValue(rep.Versions, "cli"),
			Rules:   versionValue(rep.Versions, "rules"),
			Model:   versionValue(rep.Versions, "model"),
		},
		Report:       canonical,
		ReportSHA256: sha256Prefixed(canonical),
		Evidence:     evidenceRefs(rep.Evidence),
		Bundle:       BundleRef{Included: false},
	}
	result := &Result{
		ProtocolVersion: ProtocolVersion,
		PushID:          pushID,
		CreatedAt:       payload.CreatedAt,
		ReportSHA256:    payload.ReportSHA256,
		EvidenceCount:   len(payload.Evidence),
	}

	if cfg.IncludeEvidence {
		archive, err := buildBundleArchive(b, cfg.maxBundleBytes())
		if err != nil {
			return nil, nil, err
		}
		payload.Bundle = BundleRef{
			Included:      true,
			ContentType:   "application/gzip",
			SHA256:        sha256Prefixed(archive),
			SizeBytes:     int64(len(archive)),
			ContentBase64: base64.StdEncoding.EncodeToString(archive),
		}
		result.BundleIncluded = true
		result.BundleSize = int64(len(archive))
	}
	return payload, result, nil
}

func versionValue(versions map[string]string, key string) string {
	if value := strings.TrimSpace(versions[key]); value != "" {
		return value
	}
	if key == "model" {
		return "none"
	}
	return "unknown"
}

func evidenceRefs(entries []report.Evidence) []EvidenceRef {
	refs := make([]EvidenceRef, 0, len(entries))
	for _, entry := range entries {
		refs = append(refs, EvidenceRef{
			ID:         entry.ID,
			Source:     entry.Source,
			ContentRef: entry.ContentRef,
			SHA256:     entry.SHA256,
		})
	}
	return refs
}

func sha256Prefixed(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// newUUIDv4 returns an RFC 4122 version-4 UUID using crypto/rand.
func newUUIDv4() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	raw[6] = (raw[6] & 0x0f) | 0x40 // version 4
	raw[8] = (raw[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16]), nil
}

// bundleEntry is one deterministic tar entry.
type bundleEntry struct {
	name string
	data []byte
}

// buildBundleArchive packs incident.yaml plus every text evidence file into a
// deterministic tar.gz: entries are sorted by relative slash path, report.*
// outputs and .git are excluded, and tar headers carry a fixed zero mtime,
// uid/gid 0 and mode 0644 so repeated runs produce byte-identical archives.
func buildBundleArchive(b *bundle.Bundle, maxBytes int64) ([]byte, error) {
	files, err := b.WalkTextFiles()
	if err != nil {
		return nil, fmt.Errorf("枚举证据文件失败：%w", err)
	}
	files = append(files, bundle.IncidentFile)
	sort.Strings(files)

	seen := map[string]bool{}
	entries := make([]bundleEntry, 0, len(files))
	for _, rel := range files {
		rel = filepath.ToSlash(rel)
		if seen[rel] || rel == "report.json" || rel == "report.md" || strings.HasPrefix(rel, ".git/") {
			continue
		}
		seen[rel] = true
		data, err := os.ReadFile(filepath.Join(b.Dir, filepath.FromSlash(rel)))
		if err != nil {
			return nil, fmt.Errorf("读取证据文件 %s 失败：%w", rel, err)
		}
		entries = append(entries, bundleEntry{name: rel, data: data})
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		header := &tar.Header{
			Name:     entry.name,
			Mode:     0o644,
			Size:     int64(len(entry.data)),
			ModTime:  time.Unix(0, 0).UTC(),
			Typeflag: tar.TypeReg,
			Uid:      0,
			Gid:      0,
		}
		if err := tw.WriteHeader(header); err != nil {
			return nil, fmt.Errorf("写入证据包失败：%w", err)
		}
		if _, err := tw.Write(entry.data); err != nil {
			return nil, fmt.Errorf("写入证据包失败：%w", err)
		}
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("写入证据包失败：%w", err)
	}
	if err := gz.Close(); err != nil {
		return nil, fmt.Errorf("写入证据包失败：%w", err)
	}
	if int64(buf.Len()) > maxBytes {
		return nil, &ConfigError{Reason: fmt.Sprintf("证据包大小 %d 字节超过 --max-bundle-bytes 上限 %d 字节；请减小证据包或调大上限", buf.Len(), maxBytes)}
	}
	return buf.Bytes(), nil
}
