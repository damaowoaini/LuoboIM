import '../../../core/im/open_im_gateway.dart';

final class MineProfile {
  const MineProfile({
    required this.userId,
    required this.nickname,
    this.avatar,
  });
  final String userId;
  final String nickname;
  final Uri? avatar;
}

final class MineRepository {
  const MineRepository(this.gateway, this.generation);
  final OpenImGateway gateway;
  final int generation;
  Future<MineProfile> profile() =>
      gateway.runForSession(generation, (manager) async {
        final user = await manager.userManager.getSelfUserInfo();
        final url = Uri.tryParse(user.faceURL ?? '');
        return MineProfile(
          userId: user.userID ?? '',
          nickname: user.nickname?.trim().isNotEmpty == true
              ? user.nickname!
              : user.userID ?? '',
          avatar:
              url != null &&
                  url.scheme == 'https' &&
                  url.host.isNotEmpty &&
                  url.userInfo.isEmpty
              ? url
              : null,
        );
      });
}
