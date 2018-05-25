package lua

import "fmt"

// Contract lua智能合约的实现
type Contract struct {
	info vm.ContractInfo
	code string
	main Method
	apis map[string]Method
}

func (c *Contract) Info() vm.ContractInfo {
	return c.info
}

func (c *Contract) SetPrefix(prefix string) {
	c.info.Prefix = prefix
}
func (c *Contract) SetSender(sender vm.IOSTAccount) {
	c.info.Sender = sender
}
func (c *Contract) AddSigner(signer vm.IOSTAccount) {
	c.info.Signers = append(c.info.Signers, signer)
}

func (c *Contract) Api(apiName string) (vm.Method, error) {
	if apiName == "main" {
		return &c.main, nil
	}
	rtn, ok := c.apis[apiName]
	if !ok {
		return nil, fmt.Errorf("api %v : not found", apiName)
	}
	return &rtn, nil
}

func (c *Contract) Code() string {
	return c.code
}

func (c *Contract) Encode() []byte {
	cr := contractRaw{
		info: c.info.Encode(),
		code: []byte(c.code),
	}
	mr := methodRaw{
		name: c.main.name,
		ic:   int32(c.main.inputCount),
		oc:   int32(c.main.outputCount),
	}
	cr.methods = []methodRaw{mr}
	for _, val := range c.apis {
		mr = methodRaw{
			name: val.name,
			ic:   int32(val.inputCount),
			oc:   int32(val.outputCount),
		}
		cr.methods = append(cr.methods, mr)
	}

	b, err := cr.Marshal(nil)
	if err != nil {
		panic(err)
		return nil
	}
	return append([]byte{0}, b...)
}

func (c *Contract) Decode(b []byte) error {
	var cr contractRaw
	_, err := cr.Unmarshal(b)
	var ci vm.ContractInfo
	err = ci.Decode(cr.info)
	if err != nil {
		return err
	}
	c.info = ci
	c.code = string(cr.code)
	return err
}
func (c *Contract) Hash() []byte {
	return common.Sha256(c.Encode())
}

func NewContract(info vm.ContractInfo, code string, main Method, apis ...Method) Contract {
	c := Contract{
		info: info,
		code: code,
		main: main,
	}
	c.apis = make(map[string]Method)
	for _, api := range apis {
		c.apis[api.name] = api
	}
	return c
}
