import Flutter
import UIKit
import Security
import LocalAuthentication
import CommonCrypto

/// Dedicated local-access channel. No token storage or SDK lifecycle changes.
final class AppAccessBridge {
  static var shared: AppAccessBridge?
  private let channel: FlutterMethodChannel
  private let worker = DispatchQueue(label: "luobo.app-access.crypto")
  private let service = "com.luoboduihua.imapp.app-access.biometric.v1"
  private var context: LAContext?
  private var pending: FlutterResult?
  private var pendingRequest: Int?
  private var pendingOwner: String?
  private var pendingEnrollment = false
  private var privacyEnabled = false
  private var cover: UIView?
  private weak var window: UIWindow?

  init(messenger: FlutterBinaryMessenger) {
    channel = FlutterMethodChannel(name: "luobo/app_access", binaryMessenger: messenger)
    channel.setMethodCallHandler { [weak self] call, result in self?.handle(call, result) }
    AppAccessBridge.shared = self
  }
  private func fail(_ result: FlutterResult, _ code: String) {
    result(FlutterError(code: code, message: nil, details: nil))
  }
  private func validOwner(_ value: Any?) -> String? {
    guard let text = value as? String,
          text.range(of: "^[0-9a-f]{64}$", options: .regularExpression) != nil else { return nil }
    return text
  }
  private func handle(_ call: FlutterMethodCall, _ result: @escaping FlutterResult) {
    let args = call.arguments as? [String: Any] ?? [:]
    switch call.method {
    case "capabilities":
      let fresh = LAContext()
      var error: NSError?
      result(fresh.canEvaluatePolicy(.deviceOwnerAuthenticationWithBiometrics, error: &error))
      fresh.invalidate()
    case "derivePin": derive(args, result)
    case "enroll", "authenticate":
      guard let owner = validOwner(args["owner"]), let request = args["request"] as? Int, request >= 0 else { fail(result, "invalid"); return }
      authenticate(owner, request, call.method == "enroll", result)
    case "cancel": cancel(); result(nil)
    case "remove":
      guard let owner = validOwner(args["owner"]) else { fail(result, "invalid"); return }
      let status = SecItemDelete(query(owner) as CFDictionary)
      if status == errSecSuccess || status == errSecItemNotFound { result(nil) }
      else { fail(result, "storage") }
    case "privacy":
      guard let enabled = args["enabled"] as? Bool, let covered = args["covered"] as? Bool else { fail(result, "invalid"); return }
      privacyEnabled = enabled
      setCover(enabled && covered)
      result(nil)
    default: result(FlutterMethodNotImplemented)
    }
  }
  private func derive(_ args: [String: Any], _ result: @escaping FlutterResult) {
    guard let pin = args["pin"] as? String,
          pin.range(of: "^[0-9]{6,12}$", options: .regularExpression) != nil,
          let text = args["salt"] as? String, let salt = Data(base64Encoded: text), salt.count == 32,
          let iterations = args["iterations"] as? Int, (210000...1000000).contains(iterations) else { fail(result, "invalid"); return }
    worker.async {
      var password = Array(pin.utf8), output = [UInt8](repeating: 0, count: 32)
      let status = password.withUnsafeBytes { passwordBytes in
        salt.withUnsafeBytes { saltBytes in
          CCKeyDerivationPBKDF(CCPBKDFAlgorithm(kCCPBKDF2),
            passwordBytes.baseAddress!.assumingMemoryBound(to: Int8.self), password.count,
            saltBytes.baseAddress!.assumingMemoryBound(to: UInt8.self), salt.count,
            CCPseudoRandomAlgorithm(kCCPRFHmacAlgSHA256), UInt32(iterations), &output, 32)
        }
      }
      let encoded = Data(output).base64EncodedString()
      _ = password.withUnsafeMutableBytes { $0.initializeMemory(as: UInt8.self, repeating: 0) }
      _ = output.withUnsafeMutableBytes { $0.initializeMemory(as: UInt8.self, repeating: 0) }
      DispatchQueue.main.async {
        if status == kCCSuccess { result(encoded) }
        else { self.fail(result, "storage") }
      }
    }
  }
  private func query(_ owner: String) -> [CFString: Any] {
    return [kSecClass: kSecClassGenericPassword, kSecAttrService: service,
            kSecAttrAccount: owner, kSecAttrSynchronizable: false]
  }
  private func authenticate(_ owner: String, _ request: Int, _ enroll: Bool, _ result: @escaping FlutterResult) {
    guard pending == nil else { fail(result, "cancelled"); return }
    let fresh = LAContext()
    fresh.touchIDAuthenticationAllowableReuseDuration = 0
    fresh.localizedFallbackTitle = ""
    fresh.localizedCancelTitle = "使用解锁密码"
    fresh.localizedReason = "验证屏幕锁身份"
    var error: NSError?
    guard fresh.canEvaluatePolicy(.deviceOwnerAuthenticationWithBiometrics, error: &error) else {
      fresh.invalidate()
      fail(result, error?.code == LAError.biometryLockout.rawValue ? "locked_out" : "unavailable")
      return
    }
    if enroll {
      guard let acl = SecAccessControlCreateWithFlags(nil,
        kSecAttrAccessibleWhenPasscodeSetThisDeviceOnly, .biometryCurrentSet, nil) else {
        fresh.invalidate(); fail(result, "unavailable"); return
      }
      let deleted = SecItemDelete(query(owner) as CFDictionary)
      guard deleted == errSecSuccess || deleted == errSecItemNotFound else { fresh.invalidate(); fail(result, "storage"); return }
      var secret = [UInt8](repeating: 0, count: 32)
      guard SecRandomCopyBytes(kSecRandomDefault, secret.count, &secret) == errSecSuccess else { fresh.invalidate(); fail(result, "storage"); return }
      var add = query(owner)
      add[kSecAttrAccessControl] = acl
      add[kSecValueData] = Data(secret)
      _ = secret.withUnsafeMutableBytes { $0.initializeMemory(as: UInt8.self, repeating: 0) }
      guard SecItemAdd(add as CFDictionary, nil) == errSecSuccess else { fresh.invalidate(); fail(result, "storage"); return }
    }
    context = fresh; pending = result; pendingRequest = request
    pendingOwner = owner; pendingEnrollment = enroll
    worker.async {
      var read = self.query(owner)
      read[kSecReturnData] = true
      read[kSecUseAuthenticationContext] = fresh
      var value: CFTypeRef?
      let status = SecItemCopyMatching(read as CFDictionary, &value)
      let valid = status == errSecSuccess && (value as? Data)?.count == 32
      DispatchQueue.main.async {
        guard self.pendingRequest == request, self.context === fresh else { return }
        let code: String? = valid ? nil :
          (status == errSecUserCanceled || status == errSecInteractionNotAllowed ? "cancelled" : "unavailable")
        self.finish(code)
      }
    }
  }
  private func finish(_ error: String?) {
    guard let result = pending, let request = pendingRequest else { return }
    let owner = pendingOwner, remove = pendingEnrollment && error != nil
    pending = nil; pendingRequest = nil; pendingOwner = nil; pendingEnrollment = false
    context?.invalidate(); context = nil
    if remove, let owner { SecItemDelete(query(owner) as CFDictionary) }
    if let error { fail(result, error) } else { result(request) }
  }
  func cancel() { context?.invalidate(); finish("cancelled") }
  func inactive(_ window: UIWindow?) {
    if let window { self.window = window }
    if privacyEnabled { setCover(true) }
  }
  func background() { cancel() }
  func bind(_ window: UIWindow?) { if let window { self.window = window } }
  private func setCover(_ show: Bool) {
    guard let window else { return }
    if show && cover == nil {
      let view = UIView(frame: window.bounds)
      view.backgroundColor = UIColor(red: 0.96, green: 0.97, blue: 0.98, alpha: 1)
      view.autoresizingMask = [.flexibleWidth, .flexibleHeight]
      view.isAccessibilityElement = false
      view.accessibilityElementsHidden = true
      window.addSubview(view); cover = view
    } else if !show { cover?.removeFromSuperview(); cover = nil }
  }
}
