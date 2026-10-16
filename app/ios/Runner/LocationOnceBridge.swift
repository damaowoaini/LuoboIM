import CoreFoundation
import CoreLocation
import Flutter
import UIKit

/// Engine-owned foreground one-shot location. It exposes no WebView or send operation.
final class LocationOnceBridge: NSObject, FlutterPlugin, CLLocationManagerDelegate {
  private enum Phase { case permission, resume, fix }
  private enum Transition { case inactive, background, active }
  private final class Host {
    weak var controller: FlutterViewController?
    weak var window: UIWindow?
    weak var scene: UIWindowScene?
    let usesScene: Bool
    init(controller: FlutterViewController, window: UIWindow) {
      self.controller = controller; self.window = window
      scene = window.windowScene; usesScene = window.windowScene != nil
    }
  }
  private final class Request {
    let id: String
    let result: FlutterResult
    let deadline: TimeInterval
    let host: Host
    let manager = CLLocationManager()
    var phase: Phase = .permission
    var timer: Timer?
    init(id: String, result: @escaping FlutterResult, timeout: Int64, host: Host) {
      self.id = id; self.result = result
      self.host = host
      deadline = ProcessInfo.processInfo.systemUptime + Double(timeout) / 1000
    }
  }

  private let channel: FlutterMethodChannel
  // The registrar publishes this plugin. A strong back-reference would retain that cycle.
  private weak var registrar: FlutterPluginRegistrar?
  private var pending: Request?
  private var observers: [NSObjectProtocol] = []
  private var disposed = false

  static func register(with registrar: FlutterPluginRegistrar) {
    let bridge = LocationOnceBridge(messenger: registrar.messenger(), registrar: registrar)
    registrar.addMethodCallDelegate(bridge, channel: bridge.channel)
    // Publishing is required by Flutter's detachFromEngine contract.
    registrar.publish(bridge)
  }

  private init(messenger: FlutterBinaryMessenger, registrar: FlutterPluginRegistrar) {
    channel = FlutterMethodChannel(name: "luobo/location_once", binaryMessenger: messenger)
    self.registrar = registrar
    super.init()
    let center = NotificationCenter.default
    let transitions: [(Notification.Name, Transition, Bool)] = [
      (UIApplication.willResignActiveNotification, .inactive, false),
      (UIApplication.didEnterBackgroundNotification, .background, false),
      (UIApplication.didBecomeActiveNotification, .active, false),
      (UIScene.willDeactivateNotification, .inactive, true),
      (UIScene.didEnterBackgroundNotification, .background, true),
      (UIScene.didActivateNotification, .active, true)
    ]
    for (name, transition, fromScene) in transitions {
      observers.append(center.addObserver(forName: name, object: nil, queue: .main) { [weak self] notification in
        self?.lifecycle(notification, transition: transition, fromScene: fromScene)
      })
    }
    observers.append(center.addObserver(forName: UIApplication.willTerminateNotification, object: nil, queue: .main) { [weak self] _ in
      self?.dispose()
    })
  }

  private func requestId(_ args: [String: Any], keys: Set<String>) -> String? {
    guard Set(args.keys) == keys, let id = args["requestId"] as? String,
          id.utf8.count == 32,
          id.utf8.allSatisfy({ (48...57).contains($0) || (97...102).contains($0) }) else { return nil }
    return id
  }

  private func timeout(_ value: Any?) -> Int64? {
    guard let number = value as? NSNumber, CFGetTypeID(number) != CFBooleanGetTypeID(),
          ["s", "i", "l", "q", "S", "I", "L", "Q"].contains(String(cString: number.objCType)),
          number.int64Value >= 1000, number.int64Value <= 20000 else { return nil }
    return number.int64Value
  }

  func handle(_ call: FlutterMethodCall, result: @escaping FlutterResult) {
    guard Thread.isMainThread else {
      DispatchQueue.main.async { [weak self] in
        guard let self else { result(FlutterError(code: "unavailable", message: nil, details: nil)); return }
        self.handle(call, result: result)
      }
      return
    }
    let args = call.arguments as? [String: Any]
    if call.method == "cancel" {
      guard let args, let id = requestId(args, keys: ["requestId"]) else {
        result(FlutterError(code: "invalid_request", message: nil, details: nil)); return
      }
      let matched = pending?.id == id
      if matched, let request = pending { finish(request, error: "cancelled") }
      result(["requestId": id, "cancelled": matched]); return
    }
    guard call.method == "request" else { result(FlutterMethodNotImplemented); return }
    guard let args, let id = requestId(args, keys: ["requestId", "timeoutMs"]),
          let duration = timeout(args["timeoutMs"]) else {
      result(FlutterError(code: "invalid_request", message: nil, details: nil)); return
    }
    guard pending == nil else { result(FlutterError(code: "busy", message: nil, details: nil)); return }
    guard !disposed else { result(FlutterError(code: "unavailable", message: nil, details: nil)); return }
    // Resolve now, not at registration: implicit engines may initially have no view controller.
    guard let host = captureHost() else { result(FlutterError(code: "unavailable", message: nil, details: nil)); return }
    guard foreground(host) else { result(FlutterError(code: "cancelled", message: nil, details: nil)); return }
    guard CLLocationManager.locationServicesEnabled() else {
      result(FlutterError(code: "service_off", message: nil, details: nil)); return
    }
    let request = Request(id: id, result: result, timeout: duration, host: host)
    pending = request
    request.manager.delegate = self
    request.manager.desiredAccuracy = kCLLocationAccuracyBest
    request.manager.allowsBackgroundLocationUpdates = false
    let timer = Timer(timeInterval: Double(duration) / 1000, repeats: false) { [weak self, weak request] _ in
      guard let self, let request else { return }
      self.finish(request, error: "timeout")
    }
    request.timer = timer; RunLoop.main.add(timer, forMode: .common)
    switch request.manager.authorizationStatus {
    case .notDetermined: request.manager.requestWhenInUseAuthorization()
    case .authorizedWhenInUse, .authorizedAlways: startFix(request)
    case .denied, .restricted: finish(request, error: "permanently_denied")
    @unknown default: finish(request, error: "unavailable")
    }
  }

  private func captureHost() -> Host? {
    guard let controller = registrar?.viewController as? FlutterViewController,
          let window = controller.viewIfLoaded?.window else { return nil }
    return Host(controller: controller, window: window)
  }

  private func hostCurrent(_ host: Host) -> Bool {
    guard let controller = host.controller, let window = host.window,
          registrar?.viewController === controller,
          controller.viewIfLoaded?.window === window else { return false }
    if host.usesScene {
      guard let scene = host.scene else { return false }
      return window.windowScene === scene
    }
    // Legacy app lifecycle is valid only for this still-attached, scene-less window.
    return window.windowScene == nil
  }

  private func foreground(_ host: Host) -> Bool {
    guard hostCurrent(host) else { return false }
    if host.usesScene { return host.scene?.activationState == .foregroundActive }
    return UIApplication.shared.applicationState == .active
  }

  private func lifecycle(_ notification: Notification, transition: Transition, fromScene: Bool) {
    guard let request = pending, request.host.usesScene == fromScene else { return }
    if fromScene {
      guard let scene = request.host.scene,
            let source = notification.object as? UIWindowScene, source === scene else { return }
    }
    // A different window's scene neither resumes nor cancels this request.
    guard hostCurrent(request.host) else { finish(request, error: "cancelled"); return }
    switch transition {
    case .inactive:
      if request.phase != .permission { finish(request, error: "cancelled") }
    case .background: finish(request, error: "cancelled")
    case .active:
      if request.phase == .resume, foreground(request.host) { startFix(request) }
    }
  }

  func locationManagerDidChangeAuthorization(_ manager: CLLocationManager) {
    guard let request = pending, request.manager === manager else { return }
    guard hostCurrent(request.host) else { finish(request, error: "cancelled"); return }
    guard CLLocationManager.locationServicesEnabled() else { finish(request, error: "service_off"); return }
    switch manager.authorizationStatus {
    case .authorizedWhenInUse, .authorizedAlways:
      if request.phase == .permission {
        request.phase = .resume
        if foreground(request.host) { startFix(request) }
      }
    case .denied, .restricted: finish(request, error: "permanently_denied")
    case .notDetermined: break
    @unknown default: finish(request, error: "unavailable")
    }
  }

  private func startFix(_ request: Request) {
    guard pending === request, request.phase != .fix else { return }
    guard foreground(request.host) else { finish(request, error: "cancelled"); return }
    guard ProcessInfo.processInfo.systemUptime < request.deadline else { finish(request, error: "timeout"); return }
    guard CLLocationManager.locationServicesEnabled() else { finish(request, error: "service_off"); return }
    switch request.manager.authorizationStatus {
    case .authorizedWhenInUse, .authorizedAlways:
      request.phase = .fix
      request.manager.requestLocation()
    case .denied, .restricted: finish(request, error: "permanently_denied")
    default: finish(request, error: "denied")
    }
  }

  func locationManager(_ manager: CLLocationManager, didUpdateLocations locations: [CLLocation]) {
    guard let request = pending, request.manager === manager, request.phase == .fix else { return }
    guard foreground(request.host) else { finish(request, error: "cancelled"); return }
    guard ProcessInfo.processInfo.systemUptime < request.deadline else { finish(request, error: "timeout"); return }
    guard let location = locations.last else { finish(request, error: "unavailable"); return }
    let age = Date().timeIntervalSince(location.timestamp)
    let milliseconds = location.timestamp.timeIntervalSince1970 * 1000
    guard CLLocationCoordinate2DIsValid(location.coordinate),
          location.coordinate.latitude.isFinite, location.coordinate.longitude.isFinite,
          location.horizontalAccuracy.isFinite, location.horizontalAccuracy >= 0,
          age.isFinite, age >= -2, age <= 10,
          milliseconds.isFinite, milliseconds > 0, milliseconds <= 9007199254740991 else {
      finish(request, error: "unavailable"); return
    }
    guard CLLocationManager.locationServicesEnabled() else { finish(request, error: "service_off"); return }
    guard manager.authorizationStatus == .authorizedWhenInUse || manager.authorizationStatus == .authorizedAlways else {
      finish(request, error: "denied"); return
    }
    finish(request, value: ["requestId": request.id, "latitude": location.coordinate.latitude,
      "longitude": location.coordinate.longitude, "accuracyMeters": location.horizontalAccuracy,
      "timestampMillis": Int64(milliseconds), "coordinateSystem": "wgs84",
      "approximate": manager.accuracyAuthorization == .reducedAccuracy])
  }

  func locationManager(_ manager: CLLocationManager, didFailWithError error: Error) {
    guard let request = pending, request.manager === manager else { return }
    if !CLLocationManager.locationServicesEnabled() { finish(request, error: "service_off"); return }
    if (error as? CLError)?.code == .denied {
      let denied = manager.authorizationStatus == .denied || manager.authorizationStatus == .restricted
      finish(request, error: denied ? "permanently_denied" : "denied")
    } else { finish(request, error: "unavailable") }
  }

  private func finish(_ request: Request, error: String? = nil, value: [String: Any]? = nil) {
    guard pending === request else { return }
    pending = nil // Retire first: cleanup/delegate/Flutter reentry cannot settle this request twice.
    request.timer?.invalidate(); request.timer = nil
    request.manager.delegate = nil
    request.manager.stopUpdatingLocation()
    if let error { request.result(FlutterError(code: error, message: nil, details: nil)) }
    else if let value { request.result(value) }
    else { request.result(FlutterError(code: "unavailable", message: nil, details: nil)) }
  }

  func detachFromEngine(for registrar: FlutterPluginRegistrar) { dispose() }

  private func dispose() {
    guard Thread.isMainThread else {
      DispatchQueue.main.async { self.dispose() }; return
    }
    guard !disposed else { return }
    disposed = true
    if let request = pending { finish(request, error: "cancelled") }
    for observer in observers { NotificationCenter.default.removeObserver(observer) }
    observers.removeAll()
    channel.setMethodCallHandler(nil)
  }

  deinit { for observer in observers { NotificationCenter.default.removeObserver(observer) } }
}
