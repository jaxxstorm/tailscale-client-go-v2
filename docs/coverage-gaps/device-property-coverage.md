# Device Property Coverage

- OpenAPI device properties: `42`
- Covered in `Device`: `40`
- Missing from `Device`: `2`
- Present in `Device` but not OpenAPI: `3`

## Missing From `Device`

| Property |
| --- |
| `advertisedRoutes` |
| `multipleConnections` |

## Present In `Device` But Not OpenAPI

| Property | Source |
| --- | --- |
| `AdvertisedRoutes` | `devices.go:110` |
| `clientConnectivity.derp` | `devices.go:63` |
| `postureIdentity.hardwareAddresses` | `devices.go:79` |
