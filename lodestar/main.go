// main serves the application: the generated resource API behind browser sessions, the
// console's and the portal's built Angular bundles under /console/ and /portal/, and the
// root alone redirecting to the console.
//
// Demonstrates: impulse.bootstrapped, ci-stub.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/lodestar/app"
	"github.com/impulseframework/impulse/lodestar/pkg/brand"
	"github.com/impulseframework/impulse/lodestar/pkg/config"
	"github.com/impulseframework/impulse/lodestar/pkg/router"
	"github.com/impulseframework/impulse/resource/maintenance"
	"github.com/impulseframework/impulse/resource/server"
)

func main() {
	if err := Main(); err != nil {
		log.Fatal(err)
	}
}

func Main() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// A maintenance revision serves the maintenance page in place of the application: no
	// database, no sessions, no secrets are opened. The deploy pipeline starts one before
	// a release that replaces or interrupts the database.
	if maintenance.Requested() {
		if err := maintenance.Serve(ctx, maintenanceBrands()); err != nil {
			return errors.Wrap(err, "maintenance.Serve()")
		}

		return nil
	}

	conf, err := config.NewSiteConfiguration(ctx)
	if err != nil {
		return errors.Wrap(err, "config.NewSiteConfiguration()")
	}
	defer conf.Close()

	a := app.New(conf)
	if err := a.Start(ctx); err != nil {
		return errors.Wrap(err, "app.Start()")
	}
	if err := server.New(conf.Addr()).Start(ctx, router.New(a, router.AppHooks(a))); err != nil {
		return errors.Wrap(err, "server exited unexpectedly")
	}

	return nil
}

// maintenanceBrands are the brands the maintenance page says whose it is with, by mount
// path: under /portal it carries the name and the logo the portal declares
// (web/portal/brand.json), and everywhere else the console's (web/console/brand.json),
// the root included, since the root leads to the console. impulse brand writes both into
// pkg/brand from the declarations.
//
// Demonstrates: webapp.brand.
func maintenanceBrands() maintenance.Brands {
	return maintenance.Brands{
		"/":        brand.Console(),
		"/console": brand.Console(),
		"/portal":  brand.Portal(),
	}
}
