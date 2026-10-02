package deej

// VerifyDeviceAccess is a no-op on Windows, where COM port access isn't governed by group membership
func (d *Deej) VerifyDeviceAccess() {
	d.logger.Debug("Serial device access doesn't need verification on this platform")
}
