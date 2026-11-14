package generation

import (
	"fmt"
	"log"
	"path/filepath"
	"time"

	"github.com/go-playground/errors/v5"
)

func (r *resourceGenerator) runRPCGeneration() error {
	if r.genRPCMethods {
		r.output.registerOutput(r.rpc.Dir(), prefix)
	}

	begin := time.Now()

	if err := forEachGo(r.rpcMethods, r.generateRPCMethod); err != nil {
		return err
	}

	if err := r.generateRPCInterfaces(); err != nil {
		return err
	}

	log.Printf("Finished RPC file generation in %s", time.Since(begin))

	return nil
}

func (r *resourceGenerator) generateRPCMethod(rpc *rpcMethodInfo) error {
	home := r.homeOf(rpc.identity)
	fileName := generatedGoFileName(fileStem(rpc.Name()))
	destinationFilePath := filepath.Join(home.rpc.Dir(), fileName)

	if err := r.writeFormattedGoFile(destinationFilePath, fmt.Sprintf("rpcFileTemplate:%q", rpc.Name()), rpcFileTemplate, &rpcFileData{
		Source:    home.rpc.Dir(),
		Package:   home.rpc.Package(),
		RPCMethod: rpc,
	}); err != nil {
		return errors.Wrap(err, "writeFormattedGoFile()")
	}

	return nil
}

func (r *resourceGenerator) generateRPCHandler(rpcMethod *rpcMethodInfo) error {
	begin := time.Now()
	home := r.homeOf(rpcMethod.identity)
	fileName := generatedGoFileName(fileStem(rpcMethod.Name()))
	destinationFilePath := filepath.Join(home.dir.Dir(), fileName)

	template := rpcHandlerTemplate
	if rpcMethod.Upload != nil {
		// The multipart intake is its own frame.
		template = rpcUploadHandlerTemplate
	}

	limitExpr, limitText := r.rpcBodyLimit(rpcMethod)
	if err := r.writeFormattedGoFile(destinationFilePath, fmt.Sprintf("rcpHandlerTemplate:%q", rpcMethod.Name()), template, &rpcHandlerData{
		Source:              home.rpc.Dir(),
		LocalPackageImports: r.localPackageImports(),
		RPCMethod:           rpcMethod,
		Package:             home.dir.Package(),
		ApplicationName:     home.applicationName,
		ReceiverName:        home.receiverName,
		ResourcesPackage:    home.resources.Package(),
		BodyLimitExpr:       limitExpr,
		BodyLimitText:       limitText,
		Home:                home,
	}); err != nil {
		return errors.Wrap(err, "writeFormattedGoFile()")
	}

	log.Printf("Generated RPC handler file in %s: %s", time.Since(begin), destinationFilePath)

	return nil
}

// generateScheduledHandler writes a scheduled method's handler beside the RPC handlers:
// the frame Cloud Scheduler's verified call runs, with no session, no permission check
// and no request body.
func (r *resourceGenerator) generateScheduledHandler(method *rpcMethodInfo) error {
	begin := time.Now()
	destinationFilePath := filepath.Join(r.handler.Dir(), generatedGoFileName(fileStem(method.Name())))

	if err := r.writeFormattedGoFile(destinationFilePath, fmt.Sprintf("scheduledHandlerTemplate:%q", method.Name()), scheduledHandlerTemplate, &rpcHandlerData{
		Source:              r.rpc.Dir(),
		LocalPackageImports: r.localPackageImports(),
		RPCMethod:           method,
		Package:             r.handler.Package(),
		ApplicationName:     r.applicationName,
		ReceiverName:        r.receiverName,
		ResourcesPackage:    r.resource.Package(),
	}); err != nil {
		return errors.Wrap(err, "writeFormattedGoFile()")
	}

	log.Printf("Generated scheduled handler file in %s: %s", time.Since(begin), destinationFilePath)

	return nil
}

// generateRPCInterfaces writes each methods package's Method union: the application's RPC
// package's over its own methods, an instance's over the instance's.
func (r *resourceGenerator) generateRPCInterfaces() error {
	for _, home := range r.homes() {
		if home.rpc == "" {
			continue
		}
		destinationFile := filepath.Join(".", home.rpc.Dir(), generatedGoFileName(rpcInterfaceOutputName))

		if err := r.writeFormattedGoFile(destinationFile, "rpcInterfacesTemplate", rpcInterfacesTemplate, &rpcInterfacesData{
			Source:  home.rpc.Dir(),
			Package: home.rpc.Package(),
			Types:   r.methodsOf(home),
		}); err != nil {
			return errors.Wrap(err, "writeFormattedGoFile()")
		}
	}

	return nil
}
