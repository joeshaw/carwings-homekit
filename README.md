# carwings-homecontrol

HomeKit support for the Nissan Leaf using
[hc](https://github.com/brutella/hc) and my [Carwings Go
library](https://github.com/joeshaw/carwings).

When running, this service publishes a single HomeKit accessory
exposing three services:

1. A battery service indicating the current charge of your Leaf and
   its charging status.
1. A switch service indicating whether the Leaf is currently charging.
   If the Leaf is plugged in but not charging, you can flip this
   switch on to begin charging the vehicle.
1. A fan (v2) service for the Leaf's climate control.  Flipping this
   switch toggles the vehicle's climate control system.

After the vehicle is paired with your iOS Home app, you can control it
with any service that integrates with HomeKit, including Siri ("How
much battery does the Leaf have?") and Apple Watch.  If you have a
home hub like an Apple TV or iPad, you can control the Leaf remotely.

## Installing

The tool can be installed with:

    go get -u github.com/joeshaw/carwings-homekit

You will need to create a `config.json` file with your Carwings
username and password, like so:

```json
{
    "username": "foo@example.com",
    "password": "carwingsPassw0rd"
}
```

Then you can run the service:

    carwings-homekit -config config.json

The service will make an initial call to the Carwings service to get
updated and current battery and climate control information, and then
expose the service.

To pair, open up your Home iOS app, click the + icon, choose "Add
Accessory" and then tap "Don't have a Code or Can't Scan?"  You should
see the Leaf under "Nearby Accessories."  Tap that and enter the PIN
00102003 (or whatever you chose in `config.json`).  You should see two
entries appear in your list, one for the charging switch and one for
the climate control fan.

## Contributing

Issues and pull requests are welcome.  When filing a PR, please make
sure the code has been run through `gofmt`.

## License

Copyright 2017 Joe Shaw

`carwings-homekit` is licensed under the MIT License.  See the LICENSE file
for details.


