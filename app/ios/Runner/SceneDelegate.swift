import Flutter
import UIKit

class SceneDelegate: FlutterSceneDelegate {
  private func sceneWindow(_ scene: UIScene) -> UIWindow? {
    return (scene as? UIWindowScene)?.windows.first(where: { $0.isKeyWindow })
      ?? (scene as? UIWindowScene)?.windows.first
  }
  override func sceneWillResignActive(_ scene: UIScene) {
    AppAccessBridge.shared?.inactive(sceneWindow(scene))
    super.sceneWillResignActive(scene)
  }
  override func sceneDidEnterBackground(_ scene: UIScene) {
    AppAccessBridge.shared?.background()
    super.sceneDidEnterBackground(scene)
  }
  override func sceneDidBecomeActive(_ scene: UIScene) {
    AppAccessBridge.shared?.bind(sceneWindow(scene))
    super.sceneDidBecomeActive(scene)
  }
}
