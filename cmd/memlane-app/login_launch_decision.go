package main

const (
	coreEventClass        = uint32(0x61657674) // aevt
	openApplicationEvent  = uint32(0x6f617070) // oapp
	launchPropertyKeyword = uint32(0x70726474) // prdt
	launchedAsLoginItem   = uint32(0x6c676974) // lgit
	enumeratedDescriptor  = uint32(0x656e756d) // enum
	typeDescriptor        = uint32(0x74797065) // type
)

func shouldStartHiddenAtLogin(mygoReported, appleEventReported bool) bool {
	return mygoReported || appleEventReported
}

func isLoginLaunchPropDescriptor(descType, enumCode, typeCode uint32) bool {
	switch descType {
	case enumeratedDescriptor:
		return enumCode == launchedAsLoginItem
	case typeDescriptor:
		return typeCode == launchedAsLoginItem
	default:
		return false
	}
}
