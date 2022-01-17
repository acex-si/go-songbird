// (c) 2019-2020, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package vms

import (
	"fmt"
	"github.com/flare-foundation/flare/combinedvm"
	"github.com/flare-foundation/flare/snow/engine/common"
	"github.com/flare-foundation/flare/vms/nftfx"
	"github.com/flare-foundation/flare/vms/propertyfx"
	"github.com/flare-foundation/flare/vms/secp256k1fx"
	"sync"

	"github.com/flare-foundation/flare/api/server"
	"github.com/flare-foundation/flare/ids"
	"github.com/flare-foundation/flare/snow"
	"github.com/flare-foundation/flare/utils/constants"
	"github.com/flare-foundation/flare/utils/logging"
)

// A Factory creates new instances of a VM
type Factory interface {
	New(*snow.Context) (interface{}, error)
}

// Manager is a VM manager.
// It has the following functionality:
//   1) Register a VM factory. To register a VM is to associate its ID with a
//		 VMFactory which, when New() is called upon it, creates a new instance of that VM.
//	 2) Get a VM factory. Given the ID of a VM that has been
//      registered, return the factory that the ID is associated with.
//   3) Manage the aliases of VMs
type Manager interface {
	ids.Aliaser

	// Returns a factory that can create new instances of the VM
	// with the given ID
	GetFactory(ids.ID) (Factory, error)

	// Associate an ID with the factory that creates new instances
	// of the VM with the given ID
	RegisterFactory(ids.ID, Factory) error

	// Versions returns the versions of all the VMs that have been registered
	Versions() (map[string]string, error)
}

// Implements Manager
type manager struct {
	// Note: The string representation of a VM's ID is also considered to be an
	// alias of the VM. That is, [VM].String() is an alias for the VM, too.
	ids.Aliaser

	// Key: A VM's ID
	// Value: A factory that creates new instances of that VM
	factories map[ids.ID]Factory

	// Key: A VM's ID
	// Value: version the VM returned
	versions map[ids.ID]string

	// The node's API server.
	// [manager] adds routes to this server to expose new API endpoints/services
	apiServer *server.Server

	log logging.Logger
}

// NewManager returns an instance of a VM manager
func NewManager(apiServer *server.Server, log logging.Logger) Manager {
	return &manager{
		Aliaser:   ids.NewAliaser(),
		factories: make(map[ids.ID]Factory),
		versions:  make(map[ids.ID]string),
		apiServer: apiServer,
		log:       log,
	}
}

// Return a factory that can create new instances of the vm whose
// ID is [vmID]
func (m *manager) GetFactory(vmID ids.ID) (Factory, error) {
	if factory, ok := m.factories[vmID]; ok {
		return factory, nil
	}
	return nil, fmt.Errorf("%q was not registered as a vm", vmID)
}

// Map [vmID] to [factory]. [factory] creates new instances of the vm whose
// ID is [vmID]
func (m *manager) RegisterFactory(vmID ids.ID, factory Factory) error {
	if _, exists := m.factories[vmID]; exists {
		return fmt.Errorf("%q was already registered as a vm", vmID)
	}
	fmt.Println("Alias getting called..")
	if err := m.Alias(vmID, vmID.String()); err != nil {
		fmt.Println("RegisterFactory error 1")
		return err
	}

	m.factories[vmID] = factory

	// VMs can expose a static API (one that does not depend on the state of a
	// particular chain.) This adds to the node's API server the static API of
	// the VM with ID [vmID]. This allows clients to call the VM's static API
	// methods.

	m.log.Debug("adding static API for vm %q", vmID)

	//vmsInterface, err := factory.New(nil)
	//if err != nil {
	//	return err
	//}
	vm, err := factory.New(nil)
	if err != nil {
		fmt.Println("RegisterFactory error 2")
		return err
	}

	//var vm interface{}
	//switch vmsInterface.(type) {
	//case combinedvm.CombinedVM:
	//	vms := (vmsInterface).(combinedvm.CombinedVM) // todo Put the combinedVM in some outer package to avoid circular dependency
	//	vm = vms.Vm
	//	//vm.Version()
	//	//valVM := vms.VmVal
	//	//fmt.Println("Calling GetValidators() in vms/manager")
	//	//valVM.GetValidators(ids.ID{})
	//default:
	//	vm, err = factory.New(nil)
	//	if err != nil {
	//		return err
	//	}
	//}


	//vms := (vmsInterface).(combinedvm.CombinedVM) // todo Put the combinedVM in some outer package to avoid circular dependency
	//vm := vms.Vm
	//
	//valVM := vms.VmVal
	//valVM.GetValidators(ids.ID{})

	//commonVM, ok := vm.(common.VM)
	//if !ok {
	//	return nil
	//}
	switch vm.(type) {
	case combinedvm.CombinedVM, *secp256k1fx.Fx, *nftfx.Fx, *propertyfx.Fx, []interface {}:
		return nil
	}
	commonVM := vm.(common.VM)
	version, err := commonVM.Version()
	if err != nil {
		fmt.Println("RegisterFactory error 3")
		m.log.Error("fetching version for %q errored with: %s", vmID, err)

		if err := commonVM.Shutdown(); err != nil {
			fmt.Println("RegisterFactory error 4")
			return fmt.Errorf("shutting down VM errored with: %s", err)
		}
		return nil
	}
	m.versions[vmID] = version

	handlers, err := commonVM.CreateStaticHandlers()
	if err != nil {
		fmt.Println("RegisterFactory error 5")
		m.log.Error("creating static API endpoints for %q errored with: %s", vmID, err)

		if err := commonVM.Shutdown(); err != nil {
			return fmt.Errorf("shutting down VM errored with: %s", err)
		}
		return nil
	}

	// all static endpoints go to the vm endpoint, defaulting to the vm id
	defaultEndpoint := constants.VMAliasPrefix + vmID.String()
	// use a single lock for this entire vm
	lock := new(sync.RWMutex)
	// register the static endpoints
	for extension, service := range handlers {
		m.log.Verbo("adding static API endpoint: %s%s", defaultEndpoint, extension)
		if err := m.apiServer.AddRoute(service, lock, defaultEndpoint, extension, m.log); err != nil {
			fmt.Println("RegisterFactory error 6")
			return fmt.Errorf(
				"failed to add static API endpoint %s%s: %s",
				defaultEndpoint,
				extension,
				err,
			)
		}
	}
	return nil
}

// Versions returns the primary alias of the VM mapped to the reported version
// of the VM for all the registered VMs that reported versions.
func (m *manager) Versions() (map[string]string, error) {
	versions := make(map[string]string, len(m.versions))
	for vmID, version := range m.versions {
		alias, err := m.PrimaryAlias(vmID)
		if err != nil {
			return nil, err
		}
		versions[alias] = version
	}
	return versions, nil
}
