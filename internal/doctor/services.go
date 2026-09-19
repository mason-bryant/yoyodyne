package doctor

// Whether each part the product declares could actually start.
//
// The services section says which parts of the product run together, and the
// configuration refuses what it can check from the file alone: a Slack service
// over reporting that is off, a dashboard bound outside loopback with a
// generated token. What it cannot check is whether the thing a service needs
// from outside the file is there — the two Slack tokens in the keychain, the
// dashboard's supplied token in the store the section names — and that is what
// is asked here, one finding per service, so an operator who enabled a part
// reads whether starting the product would start it or start it into a refusal.
//
// Every finding here is a warning and never a problem, for the reason every
// Slack finding is: a part that cannot start reports nothing or serves nothing,
// and no run is stopped by it. The remedy on each is the command that stores
// what is missing.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/dashboard"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// checkServices reports each declared service: off, on with what it needs in
// place, or on with what it needs missing and the command that stores it.
func (d *diagnosis) checkServices(ctx context.Context, resolved config.Resolved) []Finding {
	services := resolved.Config.Services
	productID := resolved.Config.Product.ID
	findings := make([]Finding, 0, len(config.ServiceNames))
	for _, name := range config.ServiceNames {
		check := "service:" + string(name)
		if !services.Enabled(name) {
			findings = append(findings, Finding{
				Check:   check,
				Status:  StatusOK,
				Summary: fmt.Sprintf("the %s service is off", name),
				Detail:  fmt.Sprintf("set services.%s.enabled in %s to start it with the product", name, resolved.Path),
			})
			continue
		}
		switch name {
		case config.ServiceSlack:
			findings = append(findings, d.checkSlackService(ctx, productID))
		case config.ServiceDashboard:
			findings = append(findings, d.checkDashboardService(ctx, resolved, productID))
		default:
			// The scheduler and the maintenance pass need nothing beyond the
			// configuration that already loaded: an agent to run and a repository
			// to converge are checked above, and are checked whether or not either
			// of these is on.
			findings = append(findings, Finding{
				Check:   check,
				Status:  StatusOK,
				Summary: fmt.Sprintf("the %s service is on, and needs nothing this file does not already state", name),
			})
		}
	}
	return findings
}

// checkSlackService asks the one question the configuration could not: are
// this product's two tokens stored where the sink's launch reads them. It is
// the secrets check the Slack findings already make, read for a different
// purpose — there it says whether reporting can work, here whether the product
// can start this part — and it is asked of the same store the same way, so the
// two findings cannot disagree about one keychain.
func (d *diagnosis) checkSlackService(ctx context.Context, productID domain.ProductID) Finding {
	const check = "service:slack"
	secrets := d.checkSlackSecrets(ctx, productID)
	if secrets.Status == StatusOK {
		return Finding{
			Check:   check,
			Status:  StatusOK,
			Summary: "the slack service is on, and its tokens are stored",
			Detail:  secrets.Summary,
		}
	}
	return Finding{
		Check:   check,
		Status:  StatusWarning,
		Summary: "the slack service is on and its tokens are not stored, so starting the product would start a sink that refuses to run",
		Detail:  strings.TrimSpace(secrets.Summary + "; " + secrets.Detail),
		Remedy:  secrets.Remedy,
	}
}

// checkDashboardService asks whether the token the dashboard's entry names is
// where it says. A generated token needs nothing stored, so a loopback
// dashboard is healthy on the strength of the file; a supplied one is looked
// for in the store the entry names, and the query never produces the token —
// the keychain is asked for the item and the file for its existence and mode.
func (d *diagnosis) checkDashboardService(ctx context.Context, resolved config.Resolved, productID domain.ProductID) Finding {
	const check = "service:dashboard"
	entry := resolved.Config.Services.Dashboard
	address := fmt.Sprintf("%s:%d", entry.Bind, entry.Port)
	switch entry.Token {
	case config.DashboardTokenKeychain:
		return d.checkDashboardKeychainToken(ctx, resolved, productID, address)
	case config.DashboardTokenFile:
		return d.checkDashboardFileToken(productID, address)
	}
	return Finding{
		Check:   check,
		Status:  StatusOK,
		Summary: fmt.Sprintf("the dashboard service is on, at %s, with a token generated at each start", address),
	}
}

func (d *diagnosis) checkDashboardKeychainToken(ctx context.Context, resolved config.Resolved, productID domain.ProductID, address string) Finding {
	const check = "service:dashboard"
	secret := dashboard.TokenSecret(productID)
	if d.env.GOOS != "darwin" {
		return Finding{
			Check:   check,
			Status:  StatusWarning,
			Summary: "the dashboard service is on and its token is to come from the keychain, which this platform does not have",
			Detail:  fmt.Sprintf("services.dashboard.token is %q; a file under the state root is the store this platform has", config.DashboardTokenKeychain),
			Remedy:  fmt.Sprintf("${EDITOR:-vi} %s", shellQuote(resolved.Path)),
		}
	}
	if missing := d.missingKeychainSecrets(ctx, secret); len(missing) > 0 {
		return Finding{
			Check:   check,
			Status:  StatusWarning,
			Summary: fmt.Sprintf("the dashboard service is on and its token is not in the keychain as %s, so starting the product would start a dashboard with no token to require", secret),
			Detail:  fmt.Sprintf("the dashboard at %s reads its token from an item that carries the product, so a sibling project's cannot stand in for it", address),
			Remedy:  keychainAddCommand(missing),
		}
	}
	return Finding{
		Check:   check,
		Status:  StatusOK,
		Summary: fmt.Sprintf("the dashboard service is on, at %s, and its token is in the keychain as %s", address, secret),
	}
}

func (d *diagnosis) checkDashboardFileToken(productID domain.ProductID, address string) Finding {
	const check = "service:dashboard"
	root, err := runstate.DefaultRoot(d.getenv, d.homeDir, d.env.GOOS)
	if err != nil {
		return Finding{
			Check:   check,
			Status:  StatusWarning,
			Summary: "the dashboard service is on and the state root its token file lives under could not be found",
			Detail:  err.Error(),
			Remedy:  "export YOYODYNE_STATE_HOME=$HOME/.local/state/yoyodyne",
		}
	}
	file := dashboard.TokenFile(root, productID)
	info, err := os.Stat(file)
	switch {
	case err != nil:
		return Finding{
			Check:   check,
			Status:  StatusWarning,
			Summary: "the dashboard service is on and its token file is not there, so starting the product would start a dashboard with no token to require",
			Detail:  fmt.Sprintf("the dashboard at %s reads its token from %s", address, file),
			Remedy:  fmt.Sprintf("mkdir -p %s && (umask 077 && openssl rand -hex 32 > %s)", shellQuote(filepath.Dir(file)), shellQuote(file)),
		}
	case info.Size() == 0:
		return Finding{
			Check:   check,
			Status:  StatusWarning,
			Summary: "the dashboard service is on and its token file is empty",
			Detail:  file,
			Remedy:  fmt.Sprintf("(umask 077 && openssl rand -hex 32 > %s)", shellQuote(file)),
		}
	case info.Mode().Perm()&0o077 != 0:
		return Finding{
			Check:   check,
			Status:  StatusWarning,
			Summary: "the dashboard service's token is readable by other users on this machine",
			Detail:  fmt.Sprintf("%s is mode %04o", file, info.Mode().Perm()),
			Remedy:  fmt.Sprintf("chmod 600 %s", shellQuote(file)),
		}
	}
	return Finding{
		Check:   check,
		Status:  StatusOK,
		Summary: fmt.Sprintf("the dashboard service is on, at %s, and its token is stored", address),
		Detail:  file,
	}
}
