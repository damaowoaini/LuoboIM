import 'package:permission_handler/permission_handler.dart';

import '../domain/rtc_call.dart';

final class NativeRtcPermissions implements RtcPermissionGate {
  @override
  Future<bool> request({required bool video}) async {
    final statuses = await [
      Permission.microphone,
      if (video) Permission.camera,
    ].request();
    return statuses.values.every((status) => status.isGranted);
  }

  @override
  Future<bool> openSettings() => openAppSettings();
}
