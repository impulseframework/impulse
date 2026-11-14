package generation

import (
	"fmt"
	"log"
	"path/filepath"
	"time"

	"github.com/go-playground/errors/v5"
)

func (r *resourceGenerator) generateComputedResourceHandler(res *computedResource) error {
	begin := time.Now()
	// The handler file is plural like every other resource kind's (and like the
	// handler method it holds).
	home := r.homeOf(res.identity)
	fileName := generatedGoFileName(fileStem(r.pluralize(res.Name())))
	destinationFilePath := filepath.Join(home.dir.Dir(), fileName)

	if err := r.writeFormattedGoFile(destinationFilePath, fmt.Sprintf("computedResourceHandlerTemplate:%q", res.Name()), computedResourceHandlerTemplate, &computedHandlerData{
		Source:              home.computed.Dir(),
		LocalPackageImports: r.localPackageImports(),
		Resource:            res,
		Package:             home.dir.Package(),
		ComputedPackage:     home.computed.Package(),
		ApplicationName:     home.applicationName,
		ReceiverName:        home.receiverName,
	}); err != nil {
		return errors.Wrap(err, "writeFormattedGoFile()")
	}

	log.Printf("Generated computed resource handler file in %s: %s", time.Since(begin), destinationFilePath)

	return nil
}
