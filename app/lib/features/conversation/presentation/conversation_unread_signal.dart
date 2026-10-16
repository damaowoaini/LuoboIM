import 'package:flutter/widgets.dart';

/// Owned by the mounted shell. No credential or stale page callback is stored.
final class ConversationUnreadSignal extends ChangeNotifier {
  void request() => notifyListeners();
}

class ConversationUnreadScope extends InheritedWidget {
  const ConversationUnreadScope({super.key, required this.signal, required super.child});
  final ConversationUnreadSignal signal;
  static ConversationUnreadSignal? maybeOf(BuildContext context) =>
      context.dependOnInheritedWidgetOfExactType<ConversationUnreadScope>()?.signal;
  @override
  bool updateShouldNotify(ConversationUnreadScope oldWidget) => !identical(signal, oldWidget.signal);
}

Rect? conversationUnreadBounds(BuildContext? element) {
  if (element == null || !element.mounted) return null;
  final object = element.findRenderObject();
  if (object is! RenderBox || !object.attached || !object.hasSize) return null;
  return object.localToGlobal(Offset.zero) & object.size;
}

/// Used by the real inbox and tested against real variable-height sliver rows.
/// Progress is viewport-relative; an estimated row/index offset is not success.
Future<bool> revealUnreadConversation({required String id,
    required ScrollController scroll, required Map<String, BuildContext> rowContexts,
    required BuildContext? Function() viewport,
    required List<String> Function() orderedIds,
    required bool Function() current}) async {
  while (current()) {
    await WidgetsBinding.instance.endOfFrame;
    if (!current() || !scroll.hasClients) return false;
    final visible = conversationUnreadBounds(viewport());
    if (visible == null || visible.height <= 0) return false;
    final row = rowContexts[id];
    if (row != null && row.mounted && conversationUnreadBounds(row) != null) {
      await Scrollable.ensureVisible(row, alignment: 0,
        duration: const Duration(milliseconds: 120), curve: Curves.easeOut);
      if (!current()) return false;
      await WidgetsBinding.instance.endOfFrame;
      final actual = conversationUnreadBounds(rowContexts[id]);
      final screen = conversationUnreadBounds(viewport());
      if (actual != null && screen != null && actual.overlaps(screen) &&
          actual.top >= screen.top - 1 && actual.top < screen.bottom) {
        return true;
      }
    }
    if (!current()) return false;
    final ids = orderedIds();
    final target = ids.indexOf(id);
    final mountedIndices = rowContexts.entries.where((entry) => conversationUnreadBounds(entry.value) != null)
        .map((entry) => ids.indexOf(entry.key)).where((index) => index >= 0).toList()..sort();
    if (target < 0 || mountedIndices.isEmpty) return false;
    final direction = target < mountedIndices.first ? -1 : 1;
    final old = scroll.offset;
    final next = (old + direction * visible.height * .8).clamp(
      scroll.position.minScrollExtent, scroll.position.maxScrollExtent).toDouble();
    if ((next - old).abs() < .1) return false;
    await scroll.animateTo(next, duration: const Duration(milliseconds: 80), curve: Curves.linear);
    if (!current()) return false;
  }
  return false;
}
