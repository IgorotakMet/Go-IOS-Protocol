package verifier

import (
	"errors"

	"fmt"

	"sync"

	"runtime/debug"

	"github.com/iost-official/Go-IOS-Protocol/core/state"
	"github.com/iost-official/Go-IOS-Protocol/core/tx"
	"github.com/iost-official/Go-IOS-Protocol/vm"
	"github.com/iost-official/Go-IOS-Protocol/vm/lua"
)

var ErrForbiddenCall = errors.New("forbidden call")

type vmHolder struct {
	vm.VM
	Lock      sync.Mutex
	IsRunning bool
}

func NewHolder(vmm vm.VM) *vmHolder {
	return &vmHolder{VM: vmm, Lock: sync.Mutex{}, IsRunning: false}
}

type vmMonitor struct {
	vms              map[string]vmHolder
	hotVM            *vmHolder
	needRestartHotVM bool
}

func newVMMonitor() vmMonitor {
	return vmMonitor{
		vms:              make(map[string]vmHolder),
		hotVM:            nil,
		needRestartHotVM: false,
	}
}

func (m *vmMonitor) StartVM(contract vm.Contract) (vm.VM, error) {
	if vmx, ok := m.vms[contract.Info().Prefix]; ok {
		return vmx.VM, nil
	}
	vmx, err := m.startVM(contract)
	if err != nil {
		return nil, err
	}
	holder := NewHolder(vmx)
	m.vms[contract.Info().Prefix] = *holder
	return m.vms[contract.Info().Prefix].VM, nil
}

func (m *vmMonitor) startVM(contract vm.Contract) (vm.VM, error) {
	switch contract.(type) {
	case *lua.Contract:
		var lvm lua.VM
		err := lvm.Prepare(m)
		if err != nil {
			return nil, err
		}
		err = lvm.Start(contract.(*lua.Contract))
		if err != nil {
			return nil, err
		}

		return &lvm, nil
	default:
		return nil, fmt.Errorf("contract not supported")
	}
}

func (m *vmMonitor) RestartVM(contract vm.Contract) (vm.VM, error) {
	if m.hotVM == nil || m.needRestartHotVM {
		m.needRestartHotVM = false
		vmx, err := m.startVM(contract)
		if err != nil {
			return nil, err
		}
		m.hotVM = NewHolder(vmx)
		return m.hotVM, nil
	}
	m.hotVM.Restart(contract)
	return m.hotVM, nil
}

func (m *vmMonitor) StopVM(contract vm.Contract) {
	holder, ok := m.vms[contract.Info().Prefix]
	if !ok {
		return
	}
	if holder.IsRunning {
		return
	}
	holder.Stop()
	delete(m.vms, string(contract.Hash()))
}

func (m *vmMonitor) Stop() {
	for _, vv := range m.vms {
		if vv.IsRunning {
			return
		}
		vv.Stop()
	}
	m.vms = make(map[string]vmHolder)
	if m.hotVM != nil {
		if m.hotVM.IsRunning {
			return
		}
		m.hotVM.Stop()
		m.needRestartHotVM = true
	}
}

func (m *vmMonitor) GetMethod(contractPrefix, methodName string) (vm.Method, *vm.ContractInfo, error) {
	var contract vm.Contract
	var err error
	vmh, ok := m.vms[contractPrefix]
	if !ok {
		contract, err = FindContract(contractPrefix)
		if err != nil {
			return nil, nil, err
		}
	} else {
		contract = vmh.VM.Contract()
	}
	rtn, err := contract.API(methodName)
	if err != nil {
		return nil, nil, err
	}

	info := contract.Info()
	return rtn, &info, nil

}

func (m *vmMonitor) Call(ctx *vm.Context, pool state.Pool, contractPrefix, methodName string, args ...state.Value) ([]state.Value, state.Pool, uint64, error) {

	if m.hotVM != nil && contractPrefix == m.hotVM.Contract().Info().Prefix {
		m.hotVM.IsRunning = true
		defer func() {
			m.hotVM.IsRunning = false
		}()

		rtn, pool2, err := m.hotVM.Call(ctx, pool, methodName, args...)

		var gas uint64
		if m.hotVM == nil {
			debug.PrintStack()
			return nil, pool, 0, errors.New("runtime error")
		} else {
			gas = m.hotVM.PC()
		}

		return rtn, pool2, gas, err
	}

	holder, ok := m.vms[contractPrefix]
	if !ok {
		contract, err := FindContract(contractPrefix)
		if err != nil {
			return nil, pool, 0, err
		}
		_, err = m.StartVM(contract)
		if err != nil {
			return nil, pool, 0, err
		}
		holder, ok = m.vms[contract.Info().Prefix]
	}
	holder.IsRunning = true
	defer func() { holder.IsRunning = false }()
	rtn, pool2, err := holder.Call(ctx, pool, methodName, args...)
	gas := holder.PC()
	return rtn, pool2, gas, err
}

// FindContract  find contract from tx database
func FindContract(contractPrefix string) (vm.Contract, error) {
	hash := vm.PrefixToHash(contractPrefix)

	txdb := tx.TxDbInstance()
	txx, err := txdb.Get(hash)
	if err != nil {
		return nil, err
	}
	return txx.Contract, nil
}
