// Code generated by MockGen. DO NOT EDIT.
// Source: github.com/iost-official/prototype/core (interfaces: TxPool)

// Package core_mock is a generated GoMock package.
package core_mock

import (
"github.com/golang/mock/gomock"
)

// MockTxPool is a mock of TxPool interface
type MockTxPool struct {
	ctrl     *gomock.Controller
	recorder *MockTxPoolMockRecorder
}

// MockTxPoolMockRecorder is the mock recorder for MockTxPool
type MockTxPoolMockRecorder struct {
	mock *MockTxPool
}

// NewMockTxPool creates a new mock instance
func NewMockTxPool(ctrl *gomock.Controller) *MockTxPool {
	mock := &MockTxPool{ctrl: ctrl}
	mock.recorder = &MockTxPoolMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use
func (m *MockTxPool) EXPECT() *MockTxPoolMockRecorder {
	return m.recorder
}

// Add mocks base method
func (m *MockTxPool) Add(arg0 core.Tx) error {
	ret := m.ctrl.Call(m, "Add", arg0)
	ret0, _ := ret[0].(error)
	return ret0
}

// Add indicates an expected call of Add
func (mr *MockTxPoolMockRecorder) Add(arg0 interface{}) *gomock.Call {
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Add", reflect.TypeOf((*MockTxPool)(nil).Add), arg0)
}

// Decode mocks base method
func (m *MockTxPool) Decode(arg0 []byte) error {
	ret := m.ctrl.Call(m, "Decode", arg0)
	ret0, _ := ret[0].(error)
	return ret0
}

// Decode indicates an expected call of Decode
func (mr *MockTxPoolMockRecorder) Decode(arg0 interface{}) *gomock.Call {
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Decode", reflect.TypeOf((*MockTxPool)(nil).Decode), arg0)
}

// Del mocks base method
func (m *MockTxPool) Del(arg0 core.Tx) error {
	ret := m.ctrl.Call(m, "Del", arg0)
	ret0, _ := ret[0].(error)
	return ret0
}

// Del indicates an expected call of Del
func (mr *MockTxPoolMockRecorder) Del(arg0 interface{}) *gomock.Call {
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Del", reflect.TypeOf((*MockTxPool)(nil).Del), arg0)
}

// Encode mocks base method
func (m *MockTxPool) Encode() []byte {
	ret := m.ctrl.Call(m, "Encode")
	ret0, _ := ret[0].([]byte)
	return ret0
}

// Encode indicates an expected call of Encode
func (mr *MockTxPoolMockRecorder) Encode() *gomock.Call {
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Encode", reflect.TypeOf((*MockTxPool)(nil).Encode))
}
