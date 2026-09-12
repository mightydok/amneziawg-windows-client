/* SPDX-License-Identifier: MIT
 *
 * Geo-split routing: IPC client calls and change notifications.
 */

package manager

import (
	"github.com/mightydok/awg-geolist"
)

type GeoChangeCallback struct {
	cb func()
}

var geoChangeCallbacks = make(map[*GeoChangeCallback]bool)

// IPCClientRegisterGeoChange registers a callback that fires whenever the geo-split
// settings or the cached list change, including when a refresh starts and ends.
func IPCClientRegisterGeoChange(cb func()) *GeoChangeCallback {
	s := &GeoChangeCallback{cb}
	geoChangeCallbacks[s] = true
	return s
}

func (cb *GeoChangeCallback) Unregister() {
	delete(geoChangeCallbacks, cb)
}

func IPCServerNotifyGeoChange() {
	notifyAll(GeoChangeNotificationType, false)
}

func IPCClientGeoStatus() (status GeoStatus, err error) {
	rpcMutex.Lock()
	defer rpcMutex.Unlock()

	err = rpcEncoder.Encode(GeoStatusMethodType)
	if err != nil {
		return
	}
	err = rpcDecoder.Decode(&status)
	if err != nil {
		return
	}
	err = rpcDecodeError()
	return
}

func IPCClientGeoSetSettings(settings geolist.Settings) (err error) {
	rpcMutex.Lock()
	defer rpcMutex.Unlock()

	err = rpcEncoder.Encode(GeoSetSettingsMethodType)
	if err != nil {
		return
	}
	err = rpcEncoder.Encode(settings)
	if err != nil {
		return
	}
	err = rpcDecodeError()
	return
}

func IPCClientGeoRefresh() (err error) {
	rpcMutex.Lock()
	defer rpcMutex.Unlock()

	err = rpcEncoder.Encode(GeoRefreshMethodType)
	if err != nil {
		return
	}
	err = rpcDecodeError()
	return
}

func IPCClientGeoPreview(settings geolist.Settings) (preview GeoPreview, err error) {
	rpcMutex.Lock()
	defer rpcMutex.Unlock()

	err = rpcEncoder.Encode(GeoPreviewMethodType)
	if err != nil {
		return
	}
	err = rpcEncoder.Encode(settings)
	if err != nil {
		return
	}
	err = rpcDecoder.Decode(&preview)
	if err != nil {
		return
	}
	err = rpcDecodeError()
	return
}
