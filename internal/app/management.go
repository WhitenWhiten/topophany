package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/management"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"github.com/WhitenWhiten/topophany/internal/update"
)

func (a *App) management(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("use management init, keys, enroll, inspect, enable, disable, constraints, serve, nodes, config or deployment")
	}
	switch args[0] {
	case "help", "--help", "-h":
		_, e := fmt.Fprintln(a.Out, "management init|credential|keys|enroll|inspect|enable|disable|constraints|revert-local|serve|nodes|config|deployment|update|release|helper\nConfiguration and updates are independently disabled until locally enabled. Use each command with --help; see docs/MANAGEMENT.md.")
		return e

	case "credential":
		f := a.flags("management credential")
		out := f.String("output", "", "new private credential JSON path")
		if e := parsed(f, args[1:]); e != nil {
			return e
		}
		if _, e := os.Lstat(*out); !os.IsNotExist(e) {
			return errors.New("credential output already exists")
		}
		token, e := secure.Token()
		if e != nil {
			return e
		}
		if e = management.WriteLocal(*out, management.Marshal(management.Credential{Token: token})); e != nil {
			return e
		}
		return printJSON(a.Out, map[string]string{"credential_file": *out, "token_sha256": secure.Hash(token)})

	case "release":
		return a.releaseManagement(ctx, args[1:])
	case "helper":
		f := a.flags("management helper")
		path := f.String("registration", "", "administrator-owned helper registration")
		if e := parsed(f, args[1:]); e != nil {
			return e
		}
		return update.StartHelper(ctx, *path)

	case "revert-local":
		f := a.flags("management revert-local")
		policyPath := f.String("policy", "", "local policy with configuration acceptance disabled")
		data := f.String("data", "", "stopped installation data directory")
		configPath := f.String("config", "", "reviewed local topology to resume")
		if e := parsed(f, args[1:]); e != nil {
			return e
		}
		policy, e := management.LoadPolicy(*policyPath)
		if e != nil {
			return e
		}
		if policy.Config.Accept {
			return errors.New("disable configuration acceptance before reverting")
		}
		c, e := config.Load(*configPath)
		if e != nil {
			return e
		}
		if c.Project != policy.Project {
			return errors.New("local topology project differs")
		}
		raw, e := management.ReadLocal(filepath.Join(*data, "management", "state.json"), 1<<20, false, true)
		if e != nil {
			return e
		}
		var state management.State
		if e = management.Decode(raw, &state, 1<<20); e != nil {
			return e
		}
		store, e := management.Open(filepath.Join(*data, "management"), policy, state.Epoch)
		if e != nil {
			return errors.New("stop the installation before reverting configuration")
		}
		defer store.Close()
		if e = store.RevertConfig(); e != nil {
			return e
		}
		return printJSON(a.Out, map[string]string{"configuration_source": "local", "next_step": "restart with the reviewed --config path"})
	case "keys":
		f := a.flags("management keys")
		dir := f.String("dir", "", "new private directory for controller keys")
		id := f.String("controller-id", "", "controller identity")
		if e := parsed(f, args[1:]); e != nil {
			return e
		}
		if !filepath.IsAbs(*dir) || !config.ID(*id) {
			return errors.New("absolute --dir and --controller-id required")
		}
		if _, e := os.Lstat(*dir); !os.IsNotExist(e) {
			return errors.New("key directory must not exist")
		}
		pub, key, e := ed25519.GenerateKey(rand.Reader)
		if e != nil {
			return e
		}
		if e = secure.MkdirAll(*dir, 0700); e != nil {
			return e
		}
		keyID, e := secure.Token()
		if e != nil {
			return e
		}
		trust := management.Trust{Schema: 1, ID: *id, Epoch: 1, Threshold: 1, Expires: time.Now().UTC().AddDate(1, 0, 0), Keys: []management.Key{{ID: keyID, Public: base64.RawURLEncoding.EncodeToString(pub), Roles: []string{"config", "update"}}}}
		if e = management.WriteLocal(filepath.Join(*dir, "signing-key.json"), management.Marshal(management.SigningKey{ID: keyID, Private: base64.RawURLEncoding.EncodeToString(key)})); e != nil {
			return e
		}
		if e = management.WriteLocal(filepath.Join(*dir, "trust.json"), management.Marshal(trust)); e != nil {
			return e
		}
		return printJSON(a.Out, map[string]string{"controller_id": *id, "key_id": keyID, "public_key_sha256": management.Digest(pub), "trust_file": filepath.Join(*dir, "trust.json")})
	case "init":
		f := a.flags("management init")
		path := f.String("policy", "", "new absolute local policy path")
		if e := parsed(f, args[1:]); e != nil {
			return e
		}
		if _, e := os.Lstat(*path); !os.IsNotExist(e) {
			return errors.New("policy already exists")
		}
		if e := management.WritePolicy(*path, management.Disabled()); e != nil {
			return e
		}
		return printJSON(a.Out, management.Disabled())
	case "enroll":
		f := a.flags("management enroll")
		path := f.String("policy", "", "existing local policy path")
		project := f.String("project", "", "project")
		observer := f.String("observer", "", "observer")
		role := f.String("role", "worker", "worker or collector")
		installation := f.String("installation", "", "installation ID (random if omitted)")
		controller := f.String("controller-id", "", "controller ID")
		server := f.String("server", "", "HTTPS management origin")
		ca := f.String("ca-file", "", "private CA file")
		credentials := f.String("credential-file", "", "private management credential JSON")
		trust := f.String("trust-file", "", "locally verified controller trust")
		limits := f.String("constraints-file", "", "reviewed configuration constraints")
		system := f.Bool("system", false, "require administrator-owned policy and trust")
		updatePolicy := f.String("update-policy-file", "", "reviewed local software update policy")
		publisher := f.String("publisher-trust-file", "", "locally installed official publisher trust root")
		if e := parsed(f, args[1:]); e != nil {
			return e
		}
		p, e := management.LoadPolicy(*path)
		if e != nil {
			return e
		}
		if p.Network() || p.Installation != "" {
			return errors.New("disable and review existing enrollment before replacement")
		}
		if *installation == "" {
			*installation, e = secure.Token()
			if e != nil {
				return e
			}
		}
		p.Schema = 1
		p.System = *system
		p.Project = *project
		p.Role = *role
		p.Observer = *observer
		p.Installation = *installation
		p.Controller = management.ControllerRef{ID: *controller, URL: *server, CAFile: *ca, CredentialFile: *credentials, CommandTrustFile: *trust}
		p.Config.ConstraintsFile = *limits
		p.Updates.PolicyFile = *updatePolicy
		p.Updates.PublisherTrustFile = *publisher
		p.Revision++
		// Validate transport and binding without granting acceptance or making requests.
		check := p
		check.Report = true
		if e = check.Validate(); e != nil {
			return e
		}
		t, e := management.LoadTrust(*trust, *system, time.Now())
		if e != nil {
			return e
		}
		if t.ID != *controller {
			return errors.New("controller trust identity differs")
		}
		if e = validateManagementEnrollment(check); e != nil {
			return e
		}
		if e = management.WritePolicy(*path, p); e != nil {
			return e
		}
		return printJSON(a.Out, p)
	case "inspect", "enable", "disable":
		f := a.flags("management " + args[0])
		path := f.String("policy", "", "absolute local policy path")
		capability := f.String("capability", "", "config, updates or status")
		topology := f.String("config", "", "reviewed local topology, required to enable configuration")
		if e := parsed(f, args[1:]); e != nil {
			return e
		}
		p, e := management.LoadPolicy(*path)
		if e != nil {
			return e
		}
		if args[0] == "inspect" {
			return printJSON(a.Out, p)
		}
		enabled := args[0] == "enable"
		session, e := secure.Token()
		if e != nil {
			return e
		}
		switch *capability {
		case "config":
			p.Config.Accept = enabled
			p.Config.Session = session
		case "updates":
			if enabled {
				if runtime.GOOS != "linux" || !p.System {
					return errors.New("managed updates require a registered Linux system installation")
				}
				if _, e = update.LoadPolicy(p.Updates.PolicyFile, true); e != nil {
					return e
				}
				if _, e = update.LoadRoot(p.Updates.PublisherTrustFile, true); e != nil {
					return e
				}
			}
			p.Updates.Accept = enabled
			p.Updates.Session = session
		case "status":
			p.Report = enabled
		default:
			return errors.New("choose --capability config, updates or status explicitly")
		}
		p.Revision++
		if e = p.Validate(); e != nil {
			return e
		}
		if enabled {
			t, e := management.LoadTrust(p.Controller.CommandTrustFile, p.System, time.Now())
			if e != nil {
				return e
			}
			if t.ID != p.Controller.ID {
				return errors.New("controller trust mismatch")
			}
			if e = validateManagementEnrollment(p); e != nil {
				return e
			}
			if *capability == "updates" {
				if e = update.CheckHelper(ctx, p); e != nil {
					return e
				}
			}
			if *capability == "config" {
				c, e := config.Load(*topology)
				if e != nil {
					return e
				}
				v, e := management.LoadConstraints(p.Config.ConstraintsFile, p.System)
				if e != nil {
					return e
				}
				if e = v.Check(c, p.Observer, 4); e != nil {
					return e
				}
			}
		}
		if e = management.WritePolicy(*path, p); e != nil {
			return e
		}
		return printJSON(a.Out, map[string]any{"policy": p, "runtime_confirmation": "pending; acceptance is rechecked at each operation"})
	case "constraints":
		f := a.flags("management constraints")
		path := f.String("config", "", "reviewed topology")
		observer := f.String("observer", "", "observer")
		out := f.String("output", "", "new absolute constraints file")
		cidrs := f.String("allow-cidrs", "", "explicit comma-separated destination networks for reviewed hostnames")
		allowDisable := f.Bool("allow-disable", false, "permit reviewed checks to be disabled or removed")
		also := f.String("also-configs", "", "additional reviewed topologies to authorize, comma separated")
		if e := parsed(f, args[1:]); e != nil {
			return e
		}
		c, e := config.Load(*path)
		if e != nil {
			return e
		}
		options := management.Constraints{AllowDisable: *allowDisable}
		if *cidrs != "" {
			options.CIDRs = strings.Split(*cidrs, ",")
		}
		v, e := management.ConstraintsFor(c, *observer, options)
		if e != nil {
			return e
		}
		if *also != "" {
			for _, reviewed := range strings.Split(*also, ",") {
				next, e := config.Load(reviewed)
				if e != nil {
					return e
				}
				allowed, e := management.ConstraintsFor(next, *observer, options)
				if e != nil {
					return e
				}
				if e = v.Merge(allowed); e != nil {
					return e
				}
			}
		}
		if _, e = os.Lstat(*out); !os.IsNotExist(e) {
			return errors.New("constraints output already exists")
		}
		if e = management.WriteLocal(*out, management.Marshal(v)); e != nil {
			return e
		}
		return printJSON(a.Out, v)
	case "serve":
		f := a.flags("management serve")
		path := f.String("policy", "", "controller policy")
		if e := parsed(f, args[1:]); e != nil {
			return e
		}
		p, e := management.LoadControllerPolicy(*path)
		if e != nil {
			return e
		}
		if !p.Enabled {
			return errors.New("control plane is disabled in local policy")
		}
		c, e := management.NewController(p)
		if e != nil {
			return e
		}
		defer c.Close()
		configureUpdateController(c)
		return management.ServeController(ctx, c)
	case "nodes", "config", "deployment", "update":
		return a.managementRequest(ctx, args)
	default:
		return errors.New("unknown management command")
	}
}
func (a *App) managementRequest(ctx context.Context, args []string) error {
	operation := args[0]
	args = args[1:]
	route := "/management/v1/nodes"
	method := "GET"
	if operation != "nodes" {
		if len(args) == 0 {
			return errors.New("choose config plan|publish or deployment activate|cancel")
		}
		action := args[0]
		args = args[1:]
		method = "POST"
		switch operation + "/" + action {
		case "config/plan":
			route = "plan"
		case "update/publish":
			route = "/management/v1/update-deployments"
		case "update/activate":
			route = "/management/v1/update/activate"
		case "update/cancel":
			route = "/management/v1/update/cancel"
		case "config/publish":
			route = "/management/v1/config-deployments"
		case "deployment/quiesce":
			route = "/management/v1/quiesce"
		case "deployment/activate":
			route = "/management/v1/activate"
		case "deployment/cancel":
			route = "/management/v1/cancel"
		default:
			return errors.New("unsupported management operation")
		}
	}
	f := a.flags("management " + operation)
	server := f.String("server", "", "HTTPS management origin")
	credentials := f.String("credential-file", "", "private management credential JSON")
	ca := f.String("ca-file", "", "CA file")
	request := f.String("request-file", "", "reviewed private deployment JSON; no credentials")
	if e := parsed(f, args); e != nil {
		return e
	}
	var body []byte
	var e error
	if method == "POST" {
		body, e = management.ReadLocal(*request, 4<<20, false, true)
		if e != nil {
			return e
		}
	}
	if route == "plan" {
		var plan management.ConfigDeployment
		if e = management.Decode(body, &plan, 4<<20); e != nil {
			return e
		}
		c, e := config.Decode(strings.NewReader(string(plan.Topology)))
		if e != nil {
			return e
		}
		return printJSON(a.Out, map[string]any{"deployment_id": plan.ID, "project": c.Project, "installations": plan.Targets, "private_revision": config.Revision(c), "contracts": config.Contracts(c), "network_request": false})
	}
	u, e := url.Parse(*server)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("management server must be an HTTPS origin")
	}
	p := management.Policy{Controller: management.ControllerRef{URL: *server, CredentialFile: *credentials, CAFile: *ca}}
	h, token, e := management.HTTP(p)
	if e != nil {
		return e
	}
	defer h.CloseIdleConnections()
	if operation == "update" {
		// Publisher verification is bounded to 30 seconds on the controller.
		// Ordinary agent polling retains its shorter transport deadlines.
		h.Timeout = 40 * time.Second
		if transport, ok := h.Transport.(*http.Transport); ok {
			transport.ResponseHeaderTimeout = 35 * time.Second
		}
	}
	raw, status, e := management.Exchange(ctx, h, token, method, strings.TrimRight(*server, "/")+route, body, 4<<20)
	if e != nil {
		return fmt.Errorf("management request failed (HTTP %d)", status)
	}
	var result json.RawMessage
	if e = management.Decode(raw, &result, 4<<20); e != nil {
		return e
	}
	return printJSON(a.Out, result)
}

// Enrollment is a local administrative action. A system service owns its secret
// credential file, while its parent and policy remain administrator-controlled.
// This preflight parses the secret without making a request or printing it.
func validateManagementEnrollment(p management.Policy) error {
	if e := p.Validate(); e != nil {
		return e
	}
	if e := management.ValidateDirectory(filepath.Dir(p.Controller.CredentialFile), p.System); e != nil {
		return e
	}
	raw, e := secure.PrivateRead(p.Controller.CredentialFile)
	if e != nil {
		return e
	}
	var credential management.Credential
	if management.Decode(raw, &credential, 4096) != nil || !management.HashOK(credential.Token) {
		return errors.New("invalid_management_credential")
	}
	if p.Controller.CAFile != "" {
		raw, e = management.ReadLocal(p.Controller.CAFile, 1<<20, p.System, false)
		if e != nil {
			return e
		}
		if !x509.NewCertPool().AppendCertsFromPEM(raw) {
			return errors.New("invalid_management_ca")
		}
	}
	return nil
}
