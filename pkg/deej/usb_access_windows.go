package deej

// verifyDeviceAccess does nothing on Windows, where COM port access isn't group-based
func (d *Deej) verifyDeviceAccess() {}
