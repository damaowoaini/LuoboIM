import '../../../core/error/app_failure.dart';
import '../../../core/im/open_im_gateway.dart';
import '../data/chat_repository.dart';
import '../domain/chat_message.dart';
import '../domain/message_privacy.dart';

/// One immutable lease for a root/descendant media action. Looking up only the
/// child ID is insufficient when the root or a sibling carries privacy facts.
final class ChatContentAccess {
  ChatContentAccess({
    required this.repository,
    required this.root,
    required this.child,
    required this.isRouteCurrent,
  }) : lease = repository.gateway.historyAccess(
         repository.generation,
         conversationId: repository.conversationId,
       ),
       privacy = MessagePrivacy.combined([
         root.privacy,
         child.privacy,
         repository.privacyFor(root),
         repository.privacyFor(child),
       ]);
  final ChatRepository repository;
  final ChatMessage root, child;
  final bool Function() isRouteCurrent;
  final MessageHistoryAccess lease;
  final MessagePrivacy privacy;
  bool get canView {
    try {
      lease.check();
      return isRouteCurrent() &&
          repository.canView(root) &&
          repository.canView(child) &&
          privacy.canViewAt(repository.now());
    } on AppFailure {
      return false;
    }
  }

  bool get canRetain =>
      canView &&
      repository.canRetain(root) &&
      repository.canRetain(child) &&
      privacy.canRetainAt(repository.now());
  void checkView() {
    lease.check();
    if (!canView) throw const AppFailure(FailureKind.permissionDenied);
  }

  void checkRetention() {
    checkView();
    if (!canRetain) throw const AppFailure(FailureKind.permissionDenied);
  }
}
