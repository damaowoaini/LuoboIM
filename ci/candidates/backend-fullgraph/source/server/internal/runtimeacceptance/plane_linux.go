//go:build linux

package runtimeacceptance

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
)

type ResourceObservation struct {
	Role          string `json:"role"`
	ID            string `json:"id"`
	ImageID       string `json:"imageID"`
	ReviewedImage string `json:"reviewedImage"`
	PID           int    `json:"PID"`
	Created       string `json:"created"`
	NetworkMode   string `json:"networkMode"`
}
type ResourcePlane struct {
	Version          string                `json:"version"`
	Nonce            string                `json:"nonce"`
	UID              int                   `json:"uid"`
	ResourceRoot     string                `json:"resourceRoot"`
	KeeperID         string                `json:"keeperID"`
	Observations     []ResourceObservation `json:"observations"`
	KafkaClusterID   string                `json:"kafkaClusterID"`
	DockerSHA256     string                `json:"dockerSHA256"`
	OutsideNamespace string                `json:"outsideNamespace"`
	MinioMode        string                `json:"minioMode"`
	MinioSource      *SourceMinio          `json:"minioSource,omitempty"`
}
type limitedOutput struct {
	bytes.Buffer
	overflow bool
}

func (b *limitedOutput) Write(p []byte) (int, error) {
	n := len(p)
	left := (2 << 20) - b.Len()
	if len(p) > left {
		b.overflow = true
		p = p[:left]
	}
	_, e := b.Buffer.Write(p)
	return n, e
}
func canonicalTool(path, pin string) error {
	actual, e := filepath.EvalSymlinks(path)
	if e != nil || actual != path {
		return ownedcontrol.ErrOwnedEvidence
	}
	raw, e := os.ReadFile(path)
	if e != nil || len(raw) > 128<<20 || digestBytes(raw) != pin {
		return ownedcontrol.ErrOwnedEvidence
	}
	return nil
}
func (p ResourcePlane) ValidateLive(ctx context.Context) error {
	if ctx == nil || p.Version != "owned-fullgraph-resource-plane-v1" || p.UID != os.Getuid() || canonicalDirectory(p.ResourceRoot) != nil || len(p.Observations) != 6 || !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(p.Nonce) || canonicalTool("/usr/bin/docker", p.DockerSHA256) != nil {
		return ownedcontrol.ErrOwnedEvidence
	}
	actualNamespace, e := os.Readlink("/proc/self/ns/net")
	if e != nil {
		return e
	}
	interfaces, e := net.Interfaces()
	if e != nil || len(interfaces) != 1 || interfaces[0].Name != "lo" || interfaces[0].Flags&net.FlagLoopback == 0 {
		return ownedcontrol.ErrOwnedEvidence
	}
	// Refuse accidentally running in the host namespace even if it has no
	// routable interfaces. The controller enters the exact keeper namespace.
	if !regexp.MustCompile(`^net:\[[0-9]+\]$`).MatchString(p.OutsideNamespace) || p.OutsideNamespace == actualNamespace {
		return ownedcontrol.ErrOwnedEvidence
	}
	if p.MinioMode != "image" && p.MinioMode != "source-elf" {
		return ownedcontrol.ErrOwnedEvidence
	}
	if (p.MinioMode == "source-elf") != (p.MinioSource != nil) {
		return ownedcontrol.ErrOwnedEvidence
	}
	if p.MinioSource != nil && p.MinioSource.Validate() != nil {
		return ownedcontrol.ErrOwnedEvidence
	}
	roles := map[string]bool{}
	ids := map[string]bool{}
	minioImage, redisImage := "", ""
	for _, row := range p.Observations {
		if roles[row.Role] || ids[row.ID] || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(row.ID) || row.PID <= 1 {
			return ownedcontrol.ErrOwnedEvidence
		}
		roles[row.Role], ids[row.ID] = true, true
		expected := "container:" + p.KeeperID
		if row.Role == "keeper" {
			expected = "none"
			if row.ID != p.KeeperID {
				return ownedcontrol.ErrOwnedEvidence
			}
		}
		if row.NetworkMode != expected || !regexp.MustCompile(`^[A-Za-z0-9./:_-]+@sha256:[0-9a-f]{64}$`).MatchString(row.ReviewedImage) {
			return ownedcontrol.ErrOwnedEvidence
		}
		command := exec.CommandContext(ctx, "/usr/bin/docker", "--host", "unix:///var/run/docker.sock", "container", "inspect", row.ID)
		command.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + os.Getenv("HOME")}
		var result limitedOutput
		command.Stdout = &result
		command.Stderr = &result
		if command.Run() != nil || result.overflow {
			return ownedcontrol.ErrOwnedEvidence
		}
		var actual []struct {
			ID      string `json:"Id"`
			Image   string
			Created string
			Path    string
			State   struct {
				Running bool
				Pid     int
			}
			Config struct {
				Image, User string
				Labels      map[string]string
			}
			HostConfig struct {
				NetworkMode string
				Privileged  bool
				Binds       []string
			}
			Mounts []struct {
				Type, Source, Destination string
				RW                        bool
			}
		}
		if json.Unmarshal(result.Bytes(), &actual) != nil || len(actual) != 1 {
			return ownedcontrol.ErrOwnedEvidence
		}
		a := actual[0]
		if a.ID != row.ID || a.Image != row.ImageID || a.Created != row.Created || !a.State.Running || a.State.Pid != row.PID || a.HostConfig.Privileged || a.HostConfig.NetworkMode != expected || a.Config.Image != row.ReviewedImage || a.Config.User != strconv.Itoa(os.Getuid())+":"+strconv.Itoa(os.Getgid()) || a.Config.Labels["luobo.fullgraph.owner"] != p.Nonce || a.Config.Labels["luobo.fullgraph.role"] != row.Role {
			return ownedcontrol.ErrOwnedEvidence
		}
		ns, e := os.Readlink("/proc/" + strconv.Itoa(row.PID) + "/ns/net")
		if e != nil || ns != actualNamespace {
			return ownedcontrol.ErrOwnedEvidence
		}
		if row.Role == "minio" {
			minioImage = row.ReviewedImage
			if p.MinioSource != nil && p.MinioSource.ValidateProcess(row.PID) != nil {
				return ownedcontrol.ErrOwnedEvidence
			}
		}
		if row.Role == "redis" {
			redisImage = row.ReviewedImage
		}
		binds := 0
		sourceMounted := false
		for _, m := range a.Mounts {
			if row.Role == "minio" && p.MinioSource != nil && m.Destination == "/source-minio/minio" {
				if m.Type != "bind" || m.RW || m.Source != p.MinioSource.Path || a.Path != "/source-minio/minio" {
					return ownedcontrol.ErrOwnedEvidence
				}
				sourceMounted = true
				continue
			}
			if m.Type == "tmpfs" {
				continue
			}
			if m.Type != "bind" || !filepath.IsAbs(m.Source) || filepath.Clean(m.Source) != m.Source {
				return ownedcontrol.ErrOwnedEvidence
			}
			rel, e := filepath.Rel(p.ResourceRoot, m.Source)
			if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return ownedcontrol.ErrOwnedEvidence
			}
			if m.Destination == "/task" {
				if m.Source != filepath.Join(p.ResourceRoot, "private") || m.RW {
					return ownedcontrol.ErrOwnedEvidence
				}
			} else if m.Destination == "/owned-data" {
				if m.Source != filepath.Join(p.ResourceRoot, row.Role+"-data") || !m.RW {
					return ownedcontrol.ErrOwnedEvidence
				}
			} else {
				return ownedcontrol.ErrOwnedEvidence
			}
			binds++
		}
		if binds != 2 || (row.Role == "minio" && p.MinioSource != nil && !sourceMounted) {
			return ownedcontrol.ErrOwnedEvidence
		}
	}
	if p.MinioSource != nil && minioImage != redisImage {
		return ownedcontrol.ErrOwnedEvidence
	}
	for _, role := range []string{"keeper", "mongo", "redis", "zookeeper", "kafka", "minio"} {
		if !roles[role] {
			return ownedcontrol.ErrOwnedEvidence
		}
	}
	return nil
}
