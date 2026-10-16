import '../../../core/storage/remote_attachment_store.dart';

// Preserve the feature's existing public API while sharing account-fenced downloads.
typedef FavoriteAttachmentStore = RemoteAttachmentStore;
String safeFavoriteFilename(String name) {
  final safe = safeAttachmentFilename(name);
  return safe == '附件.bin' ? '收藏附件.bin' : safe;
}
