package localfs

import "io"

// Policy is immutable and scoped to one session. Its zero value (and nil)
// retains the existing personal-account policy. It never changes permissions.
// Construct service policies only after binding the service name to its SID.
type Policy struct{ serviceSID, operatorSID string }

func (p *Policy) IsService() bool { return p != nil && p.serviceSID != "" }

func (p *Policy) CheckReadOnlyFile(path string) error {
	pin, err := p.PinReadOnlyFile(path)
	if err != nil {
		return err
	}
	return pin.Close()
}
func (p *Policy) CheckMutableFile(path string) error {
	pin, err := p.pinObject(path, false, true, false)
	if err != nil {
		return err
	}
	return pin.Close()
}
func (p *Policy) CheckMutableDirectory(path string) error {
	pin, err := p.pinObject(path, true, true, false)
	if err != nil {
		return err
	}
	return pin.Close()
}

// PinReadOnlyFile retains the checked file and every ancestor against replacement.
func (p *Policy) PinReadOnlyFile(path string) (io.Closer, error) {
	return p.pinObject(path, false, false, false)
}
func (p *Policy) PinReadOnlyDirectory(path string) (io.Closer, error) {
	return p.pinObject(path, true, false, false)
}
