//go:build linux

package update

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/WhitenWhiten/topophany/internal/management"
	"github.com/WhitenWhiten/topophany/internal/secure"
)

var serviceName = regexp.MustCompile(`^topophany-[A-Za-z0-9][A-Za-z0-9_-]{0,63}\.service$`)

type linuxEffects struct {
	reg      Registration
	uid, gid uint32
}

func newLinuxEffects(reg Registration) (*linuxEffects, error) {
	if os.Geteuid() != 0 || !serviceName.MatchString(reg.Service) {
		return nil, errors.New("helper_requires_root_and_registered_topophany_service")
	}
	for _, marker := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(marker); err == nil {
			return nil, errors.New("container_updates_require_external_deployment")
		}
	}
	for _, prefix := range []string{"/usr/bin/", "/usr/sbin/", "/bin/", "/sbin/", "/nix/store/"} {
		if strings.HasPrefix(reg.Binary, prefix) {
			return nil, errors.New("system_package_path_not_supported")
		}
	}
	for _, query := range [][]string{{"/usr/bin/dpkg-query", "--search", reg.Binary}, {"/usr/bin/rpm", "-qf", reg.Binary}} {
		if _, err := os.Stat(query[0]); err == nil {
			check, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			cmd := exec.CommandContext(check, query[0], query[1:]...)
			cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C"}
			err = cmd.Run()
			cancel()
			if err == nil {
				return nil, errors.New("package_managed_binary_not_supported")
			}
		}
	}
	u, e := user.Lookup(reg.User)
	if e != nil {
		return nil, errors.New("helper_service_user_missing")
	}
	uid, e := strconv.ParseUint(u.Uid, 10, 32)
	if e != nil || uid == 0 {
		return nil, errors.New("helper_unprivileged_user_required")
	}
	gid, e := strconv.ParseUint(u.Gid, 10, 32)
	if e != nil || gid == 0 {
		return nil, errors.New("helper_unprivileged_group_required")
	}
	for _, path := range []string{reg.PolicyFile, reg.Binary, reg.BootstrapManifest} {
		if e = management.ValidateLocalFile(path, true, false); e != nil {
			return nil, e
		}
	}
	if e = management.ValidateDirectory(filepath.Dir(reg.Data), true); e != nil {
		return nil, e
	}
	st, e := os.Lstat(reg.Data)
	if e != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("helper_data_directory_invalid")
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(uid) {
		return nil, errors.New("helper_data_owner_mismatch")
	}
	return &linuxEffects{reg, uint32(uid), uint32(gid)}, nil
}
func (e *linuxEffects) systemctl(ctx context.Context, action string) error {
	cmd := exec.CommandContext(ctx, "/usr/bin/systemctl", action, e.reg.Service)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C"}
	if cmd.Run() != nil {
		return errors.New("registered_service_operation_failed")
	}
	return nil
}
func (e *linuxEffects) Stop(ctx context.Context) error  { return e.systemctl(ctx, "stop") }
func (e *linuxEffects) Start(ctx context.Context) error { return e.systemctl(ctx, "start") }
func (e *linuxEffects) runAsUser(ctx context.Context, bin, wantConfig string, args ...string) error {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "HOME=/nonexistent"}
	cmd.Dir = e.reg.Data
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: e.uid, Gid: e.gid, Groups: []uint32{e.gid}}}
	cmd.WaitDelay = 2 * time.Second
	var output recoveryOutput
	cmd.Stdout = &output
	if cmd.Run() != nil {
		return errors.New("candidate_recovery_check_failed")
	}
	var fields map[string]json.RawMessage
	if management.Decode(output.Bytes(), &fields, 16<<10) != nil {
		return errors.New("candidate_recovery_report_invalid")
	}
	var report struct {
		Recoverable bool   `json:"recoverable"`
		Revision    string `json:"private_config_revision"`
	}
	if json.Unmarshal(output.Bytes(), &report) != nil || !report.Recoverable || report.Revision != wantConfig {
		return errors.New("candidate_recovery_configuration_mismatch")
	}
	return nil
}

type recoveryOutput struct{ bytes.Buffer }

func (b *recoveryOutput) Write(p []byte) (int, error) {
	if len(p) > 16<<10-b.Len() {
		return 0, errors.New("candidate_recovery_report_budget")
	}
	return b.Buffer.Write(p)
}

func (e *linuxEffects) Preflight(ctx context.Context, candidate, stateDir, wantConfig string) error {
	// The durable original remains untouched. All candidate writes occur in a
	// private disposable copy, under the service identity, after signature checks.
	backup := filepath.Join(stateDir, "recovery-copy")
	_ = os.RemoveAll(backup)
	if err := os.Mkdir(backup, 0700); err != nil {
		return err
	}
	root, err := os.OpenRoot(e.reg.Data)
	if err != nil {
		return err
	}
	defer root.Close()
	lockFile, err := root.OpenFile("writer.lock", os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return errors.New("service_writer_lock_unavailable")
	}
	defer lockFile.Close()
	lockInfo, err := lockFile.Stat()
	if err != nil || !lockInfo.Mode().IsRegular() {
		return errors.New("service_writer_lock_not_regular")
	}
	if err = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("service_writer_still_active")
	}
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
	var bytes int64
	files := 0
	entries := 0
	err = fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == "." {
			return nil
		}
		entries++
		if entries > 100000 {
			return errors.New("recovery_copy_budget")
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("recovery_symlink_refused")
		}
		info, err := root.Lstat(path)
		if err != nil {
			return err
		}
		dst := filepath.Join(backup, filepath.FromSlash(path))
		if entry.IsDir() {
			return os.Mkdir(dst, 0700)
		}
		if !info.Mode().IsRegular() {
			return errors.New("recovery_nonregular_file")
		}
		files++
		bytes += info.Size()
		if files > 100000 || bytes > 1<<30 {
			return errors.New("recovery_copy_budget")
		}
		in, err := root.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if err != nil {
			return err
		}
		actual, statErr := in.Stat()
		if statErr != nil || !actual.Mode().IsRegular() {
			in.Close()
			return errors.New("recovery_file_changed")
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			in.Close()
			return err
		}
		n, copyErr := io.Copy(out, io.LimitReader(in, info.Size()+1))
		in.Close()
		if copyErr == nil && n != info.Size() {
			copyErr = errors.New("recovery_file_changed")
		}
		if copyErr == nil {
			copyErr = out.Sync()
		}
		out.Close()
		return copyErr
	})
	if err != nil {
		return err
	}
	if err = secure.SyncDir(backup); err != nil {
		return err
	}
	// Make only the disposable copy and a verified candidate accessible to the
	// service. Root-owned recovery authority/journals remain inaccessible.
	trial, err := os.MkdirTemp(filepath.Dir(e.reg.Data), ".topophany-recovery-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(trial)
	if err = os.Chmod(trial, 0711); err != nil {
		return err
	}
	trialBinary := filepath.Join(trial, "topo")
	hash, err := fileDigest(candidate, true)
	if err != nil {
		return err
	}
	if err = replaceBinary(candidate, trialBinary, hash, true); err != nil {
		return err
	}
	trialData := filepath.Join(trial, "data")
	if err = os.Rename(backup, trialData); err != nil {
		return err
	}
	if err = handoffRecoveryCopy(trialData, e.uid, e.gid); err != nil {
		return err
	}
	configPath := e.reg.Config
	stateFile, err := root.OpenFile("management/state.json", os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	var stateRaw []byte
	if err == nil {
		st, statErr := stateFile.Stat()
		if statErr != nil || !st.Mode().IsRegular() {
			stateFile.Close()
			return errors.New("managed_state_not_regular")
		}
		stateRaw, err = readBounded(stateFile, 1<<20)
		stateFile.Close()
	}
	if err != nil && !os.IsNotExist(err) {
		return errors.New("managed_state_unavailable")
	}
	if err == nil {
		var state management.State
		if management.Decode(stateRaw, &state, 1<<20) != nil {
			return errors.New("invalid_managed_state")
		}
		if state.Config.ActiveDigest != "" {
			if !management.HashOK(state.Config.ActiveDigest) {
				return errors.New("invalid_active_config")
			}
			configPath = filepath.Join(trialData, "management", state.Config.ActiveDigest+".json")
		}
	}
	role := e.reg.Role
	args := []string{"recovery", "--role", role, "--data", trialData, "--config", configPath}
	if role == "collector" && e.reg.Auth != "" {
		args = append(args, "--auth", e.reg.Auth)
	}
	return e.runAsUser(ctx, trialBinary, wantConfig, args...)
}

// Transfer the private rehearsal tree without exposing its parent to the service
// before privileged traversal is finished. Chown never follows a leaf symlink;
// directories are handed over bottom-up, with the root's 0700 gate last.
func handoffRecoveryCopy(path string, uid, gid uint32) error {
	if err := management.ValidateDirectory(path, true); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode().Perm() != 0700 {
		return errors.New("trial_root_must_be_private")
	}
	dirs := []string{}
	err = filepath.WalkDir(path, func(name string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("trial_symlink_refused")
		}
		if d.IsDir() {
			dirs = append(dirs, name)
			return nil
		}
		f, err := os.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if err != nil {
			return err
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil || !st.Mode().IsRegular() {
			return errors.New("trial_file_not_regular")
		}
		return f.Chown(int(uid), int(gid))
	})
	if err != nil {
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		f, err := os.OpenFile(dirs[i], os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_DIRECTORY, 0)
		if err != nil {
			return err
		}
		err = f.Chown(int(uid), int(gid))
		f.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
func (e *linuxEffects) Healthy(ctx context.Context, version, digest string) error {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		cmd := exec.CommandContext(ctx, "/usr/bin/systemctl", "show", "--property=MainPID", "--value", e.reg.Service)
		cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C"}
		raw, err := cmd.Output()
		if err == nil {
			pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
			if err == nil && pid > 1 {
				f, err := os.Open(fmt.Sprintf("/proc/%d/exe", pid))
				if err == nil {
					actual, err := digestReader(f)
					f.Close()
					if err == nil && actual == digest {
						status, err := e.runtimeRecord()
						if err == nil && status.PID == pid && NormalizeInstalledVersion(status.Version) == version {
							return nil
						}
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

// StartHelper is a locally installed service, never launched through sudo by a worker.
func StartHelper(ctx context.Context, registrationPath string) error {
	raw, err := management.ReadLocal(registrationPath, 64<<10, true, true)
	if err != nil {
		return err
	}
	var reg Registration
	if err = management.Decode(raw, &reg, 64<<10); err != nil {
		return err
	}
	effects, err := newLinuxEffects(reg)
	if err != nil {
		return err
	}
	source := NewGitHub()
	defer source.Close()
	helper, err := openHelper(reg, effects, source, true)
	if err != nil {
		return err
	}
	defer helper.Close()
	if err = helper.Recover(ctx); err != nil {
		return err
	}
	p, err := management.LoadPolicy(reg.PolicyFile)
	if err != nil {
		return err
	}
	policy, err := LoadPolicy(p.Updates.PolicyFile, true)
	if err != nil {
		return err
	}
	dir := filepath.Dir(policy.Socket)
	if err = management.MakePrivateDirectory(dir, true); err != nil {
		return err
	}
	if err = os.Chmod(dir, 0755); err != nil {
		return err
	}
	if st, err := os.Lstat(policy.Socket); err == nil {
		if st.Mode()&os.ModeSocket == 0 {
			return errors.New("helper_socket_not_socket")
		}
		owner, ok := st.Sys().(*syscall.Stat_t)
		if !ok || owner.Uid != 0 {
			return errors.New("untrusted_helper_socket")
		}
		connection, connectErr := net.DialTimeout("unix", policy.Socket, 300*time.Millisecond)
		if connectErr == nil {
			connection.Close()
			return errors.New("helper_socket_already_active")
		}
		if !errors.Is(connectErr, syscall.ECONNREFUSED) {
			return errors.New("helper_socket_state_unknown")
		}
		if err = os.Remove(policy.Socket); err != nil {
			return err
		}
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: policy.Socket, Net: "unix"})
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(policy.Socket)
	if err = os.Chown(policy.Socket, 0, int(effects.gid)); err != nil {
		return err
	}
	if err = os.Chmod(policy.Socket, 0660); err != nil {
		return err
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/status" {
			helper.mu.Lock()
			defer helper.mu.Unlock()
			if helper.state.Last == nil {
				w.WriteHeader(204)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(management.Marshal(helper.state.Last))
			return
		}
		if r.Method == "GET" && r.URL.Path == "/capabilities" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(management.Marshal(map[string]any{"schema": 1, "installation_id": reg.Installation, "protocol": 1}))
			return
		}
		if r.Method != "POST" || r.URL.Path != "/update" {
			http.Error(w, "unsupported_helper_operation", 404)
			return
		}
		raw, err := readBounded(r.Body, 256<<10)
		var req HelperRequest
		if err != nil || management.Decode(raw, &req, 256<<10) != nil {
			http.Error(w, "invalid_helper_request", 400)
			return
		}
		receipt, err := helper.Handle(r.Context(), req)
		if err != nil && receipt.CommandID == "" {
			http.Error(w, "helper_request_refused", 403)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(management.Marshal(receipt))
	})
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Minute, IdleTimeout: time.Second, MaxHeaderBytes: 8192}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			stop, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
			defer cancel()
			_ = server.Shutdown(stop)
		case <-done:
		}
	}()
	err = server.Serve(peerListener{UnixListener: listener, uid: effects.uid, slots: make(chan struct{}, 16)})
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

type peerListener struct {
	slots chan struct{}
	*net.UnixListener
	uid uint32
}

func (l peerListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.AcceptUnix()
		if err != nil {
			return nil, err
		}
		raw, err := conn.SyscallConn()
		var cred *syscall.Ucred
		if err == nil {
			err = raw.Control(func(fd uintptr) { cred, _ = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED) })
		}
		if err == nil && cred != nil && (cred.Uid == 0 || cred.Uid == l.uid) {
			select {
			case l.slots <- struct{}{}:
				return &helperConn{Conn: conn, release: func() { <-l.slots }}, nil
			default:
				conn.Close()
				continue
			}
		}
		conn.Close()
	}
}

func (e *linuxEffects) Configuration(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	record, err := e.runtimeRecord()
	return record.Revision, err
}

type runtimeRecord struct {
	Schema   int       `json:"schema"`
	Role     string    `json:"role"`
	PID      int       `json:"pid"`
	Version  string    `json:"version"`
	Project  string    `json:"project"`
	Observer string    `json:"observer"`
	Revision string    `json:"private_revision"`
	At       time.Time `json:"observed_at"`
	Until    time.Time `json:"valid_until"`
}

// The service owns its data directory. Read through a confined directory handle
// and a nonblocking, no-follow descriptor so a replaced FIFO or link cannot hang
// the privileged recovery supervisor or make it read outside the registration.
func (e *linuxEffects) runtimeRecord() (runtimeRecord, error) {
	var record runtimeRecord
	root, err := os.OpenRoot(e.reg.Data)
	if err != nil {
		return record, err
	}
	defer root.Close()
	file, err := root.OpenFile("local-runtime.json", os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return record, err
	}
	defer file.Close()
	st, err := file.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return record, errors.New("runtime_record_not_regular")
	}
	raw, err := readBounded(file, 16<<10)
	if err != nil {
		return record, err
	}
	now := time.Now()
	if management.Decode(raw, &record, 16<<10) != nil || record.Schema != 1 || record.PID <= 1 || record.Project != e.reg.Project || record.Role != e.reg.Role || record.Observer != e.reg.Observer || !management.HashOK(record.Revision) || !record.At.Before(now.Add(5*time.Second)) || !record.Until.After(now) || record.Until.Sub(record.At) != 15*time.Second {
		return record, errors.New("runtime_record_unavailable")
	}
	return record, nil
}

type helperConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *helperConn) Close() error { err := c.Conn.Close(); c.once.Do(c.release); return err }
func verifyHelperPeer(conn net.Conn) error {
	unix, ok := conn.(*net.UnixConn)
	if !ok {
		return errors.New("helper_requires_unix_socket")
	}
	raw, e := unix.SyscallConn()
	if e != nil {
		return e
	}
	var peer *syscall.Ucred
	e = raw.Control(func(fd uintptr) { peer, _ = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED) })
	if e != nil || peer == nil || peer.Uid != 0 {
		return errors.New("helper_peer_is_not_administrator")
	}
	return nil
}
