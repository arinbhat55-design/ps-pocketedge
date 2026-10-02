import Cocoa
import FlutterMacOS

class MainFlutterWindow: NSWindow {
  override func awakeFromNib() {
    let flutterViewController = FlutterViewController()
    let windowFrame = self.frame
    self.contentViewController = flutterViewController
    self.setFrame(windowFrame, display: true)

    RegisterGeneratedPlugins(registry: flutterViewController)

    // Setup supplies a non-secret API address on the first launch. Store it
    // in this sandbox's preferences so subsequent launches use the same URL.
    let arguments = ProcessInfo.processInfo.arguments
    if let index = arguments.firstIndex(of: "--control-plane-url"),
       index + 1 < arguments.count,
       let url = URL(string: arguments[index + 1]),
       ["http", "https"].contains(url.scheme ?? ""),
       url.host != nil, url.user == nil, url.password == nil {
      UserDefaults.standard.set(arguments[index + 1], forKey: "ControlPlaneURL")
    }
    let configuration = FlutterMethodChannel(
      name: "com.pspocketedge.app/config",
      binaryMessenger: flutterViewController.engine.binaryMessenger)
    configuration.setMethodCallHandler { call, result in
      if call.method == "getControlPlaneURL" {
        result(UserDefaults.standard.string(forKey: "ControlPlaneURL"))
      } else {
        result(FlutterMethodNotImplemented)
      }
    }

    super.awakeFromNib()
  }
}
