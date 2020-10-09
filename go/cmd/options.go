package main

import (
	"github.com/go-flutter-desktop/go-flutter"

	"github.com/go-flutter-desktop/plugins/boltdb/api"
	"github.com/go-flutter-desktop/plugins/path_provider"
	"github.com/go-flutter-desktop/plugins/url_launcher"
	file_picker "github.com/miguelpruivo/flutter_file_picker/go"
)

var options = []flutter.Option{
	flutter.WindowInitialDimensions(720, 1200),
	flutter.PopBehavior(flutter.PopBehaviorClose), // on SystemNavigator.pop() closes the app

	flutter.AddPlugin(&api.DB{}),
	flutter.AddPlugin(&file_picker.FilePickerPlugin{}),
	flutter.AddPlugin(&path_provider.PathProviderPlugin{
		VendorName:      "zc310.tech",
		ApplicationName: "boltdb",
	}),
	flutter.AddPlugin(&url_launcher.UrlLauncherPlugin{}),
}
