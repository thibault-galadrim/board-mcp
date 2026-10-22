package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework LocalAuthentication -framework Foundation
#import <LocalAuthentication/LocalAuthentication.h>
#include <stdlib.h>

// Returns 1 when the owner authenticated, 0 when refused or timed out,
// -1 when the policy cannot be evaluated at all. *errOut is malloc'd.
static int authenticate(const char *reason, int biometricsOnly, int timeoutSec, char **errOut) {
	@autoreleasepool {
		LAContext *ctx = [[LAContext alloc] init];
		LAPolicy policy = biometricsOnly
			? LAPolicyDeviceOwnerAuthenticationWithBiometrics
			: LAPolicyDeviceOwnerAuthentication;
		NSError *err = nil;
		if (![ctx canEvaluatePolicy:policy error:&err]) {
			*errOut = strdup(err.localizedDescription.UTF8String);
			return -1;
		}
		__block int result = 0;
		__block NSString *msg = nil;
		dispatch_semaphore_t sem = dispatch_semaphore_create(0);
		[ctx evaluatePolicy:policy
		    localizedReason:[NSString stringWithUTF8String:reason]
		              reply:^(BOOL success, NSError *e) {
			result = success ? 1 : 0;
			if (!success && e) msg = e.localizedDescription;
			dispatch_semaphore_signal(sem);
		}];
		dispatch_time_t deadline = dispatch_time(DISPATCH_TIME_NOW, (int64_t)timeoutSec * NSEC_PER_SEC);
		if (dispatch_semaphore_wait(sem, deadline) != 0) {
			[ctx invalidate];
			dispatch_semaphore_wait(sem, DISPATCH_TIME_FOREVER);
			msg = @"timeout";
			result = 0;
		}
		if (msg) *errOut = strdup(msg.UTF8String);
		return result;
	}
}
*/
import "C"

import (
	"errors"
	"time"
	"unsafe"
)

var errUnavailable = errors.New("owner authentication unavailable")

func authenticate(reason string, biometricsOnly bool, timeout time.Duration) (bool, error) {
	cReason := C.CString(reason)
	defer C.free(unsafe.Pointer(cReason))
	var cErr *C.char
	bio := C.int(0)
	if biometricsOnly {
		bio = 1
	}
	res := C.authenticate(cReason, bio, C.int(timeout/time.Second), &cErr)
	var msg string
	if cErr != nil {
		msg = C.GoString(cErr)
		C.free(unsafe.Pointer(cErr))
	}
	switch res {
	case 1:
		return true, nil
	case -1:
		return false, errors.Join(errUnavailable, errors.New(msg))
	default:
		if msg != "" {
			return false, errors.New(msg)
		}
		return false, nil
	}
}
