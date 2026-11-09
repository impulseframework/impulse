// Package mock contains the generated mocks for the project.
package mock

//go:generate mockgen -package access -source ../access_iface.go -destination ../mock_access_iface.go
//
//go:generate mockgen -source ../access_iface.go -destination mock_access/mock_manager.go
//go:generate mockgen -source ../handler.go -destination mock_access/mock_handler.go
