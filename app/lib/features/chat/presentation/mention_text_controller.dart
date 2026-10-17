import 'package:flutter/material.dart';

import '../domain/chat_mention.dart';

/// Editable text, selection, composing and rendered spans always share the same
/// visible string. Only a frozen send projection changes @nickname to @UID.
final class MentionTextController extends TextEditingController {
  List<MentionToken> _tokens = [];
  bool _installing = false;
  List<MentionToken> get tokens => List.unmodifiable(_tokens);
  MentionDraft freeze() => MentionDraft(text, _tokens);
  String get storedDraft => freeze().encode();
  void restore(String stored) {
    final draft = MentionDraft.restore(stored);
    _install(draft.text, draft.tokens, draft.text.length);
  }

  /// Only a verified self-revoke draft supplies identities. Ordinary stored
  /// drafts keep restore()'s plain-text-only contract.
  void restoreRecalled(MentionDraft draft) {
    if (!draft.valid || draft.text.length > 20000) {
      throw const FormatException();
    }
    _install(draft.text, draft.tokens, draft.text.length);
  }

  @override
  set value(TextEditingValue next) {
    if (!_installing) {
      final old = value;
      if (old.text != next.text) {
        var prefix = 0, suffix = 0;
        while (prefix < old.text.length &&
            prefix < next.text.length &&
            old.text.codeUnitAt(prefix) == next.text.codeUnitAt(prefix)) {
          prefix++;
        }
        while (suffix < old.text.length - prefix &&
            suffix < next.text.length - prefix &&
            old.text.codeUnitAt(old.text.length - suffix - 1) ==
                next.text.codeUnitAt(next.text.length - suffix - 1)) {
          suffix++;
        }
        final oldEnd = old.text.length - suffix;
        final delta = next.text.length - old.text.length;
        _tokens = [
          for (final token in _tokens)
            if (token.end <= prefix)
              token
            else if (token.start >= oldEnd)
              token.shifted(delta),
        ];
      }
      _tokens = _tokens
          .where(
            (token) =>
                token.matches(next.text) &&
                !(next.composing.isValid &&
                    !next.composing.isCollapsed &&
                    next.composing.start < token.end &&
                    next.composing.end > token.start) &&
                (token.end == next.text.length ||
                    _boundary(next.text.codeUnitAt(token.end))),
          )
          .toList();
    }
    super.value = next;
  }

  bool _boundary(int code) =>
      code <= 32 || ',.!?:;，。！？：；、)]}）】'.codeUnits.contains(code);
  bool insert(MentionIdentity identity) {
    if (!identity.valid ||
        (!freeze().recipients.any((v) => v.userId == identity.userId) &&
            freeze().recipients.length >= maximumMentionRecipients)) {
      return false;
    }
    if (value.composing.isValid && !value.composing.isCollapsed) return false;
    var start = selection.isValid ? selection.start : text.length;
    var end = selection.isValid ? selection.end : text.length;
    // Keep an invalid platform selection from splitting a Unicode grapheme.
    final boundaries = <int>{0};
    var count = 0;
    for (final character in text.characters) {
      count += character.length;
      boundaries.add(count);
    }
    start = boundaries.where((n) => n <= start).last;
    end = boundaries.firstWhere((n) => n >= end, orElse: () => text.length);
    final label = identity.label;
    final separator = start > 0 && !_boundary(text.codeUnitAt(start - 1))
        ? ' '
        : '';
    final replacement = '$separator$label ';
    final next = text.replaceRange(start, end, replacement);
    if (next.length > 20000) return false;
    final delta = replacement.length - (end - start);
    final retained = [
      for (final token in _tokens)
        if (token.end <= start)
          token
        else if (token.start >= end)
          token.shifted(delta),
      MentionToken(
        start + separator.length,
        start + separator.length + label.length,
        identity,
      ),
    ];
    retained.sort((a, b) => a.start.compareTo(b.start));
    _install(next, retained, start + replacement.length);
    return true;
  }

  void removeIdentity(String id) {
    _tokens = _tokens.where((t) => t.identity.userId != id).toList();
    notifyListeners();
  }

  void _install(String text, Iterable<MentionToken> tokens, int caret) {
    _installing = true;
    _tokens = tokens.toList();
    value = TextEditingValue(
      text: text,
      selection: TextSelection.collapsed(offset: caret),
    );
    _installing = false;
  }

  @override
  TextSpan buildTextSpan({
    required BuildContext context,
    TextStyle? style,
    required bool withComposing,
  }) {
    if (withComposing &&
        value.composing.isValid &&
        !value.composing.isCollapsed) {
      return super.buildTextSpan(
        context: context,
        style: style,
        withComposing: withComposing,
      );
    }
    var cursor = 0;
    final spans = <InlineSpan>[];
    for (final token in _tokens) {
      spans.add(TextSpan(text: text.substring(cursor, token.start)));
      spans.add(
        TextSpan(
          text: text.substring(token.start, token.end),
          style: TextStyle(
            color: Theme.of(context).colorScheme.primary,
            fontWeight: FontWeight.w600,
          ),
        ),
      );
      cursor = token.end;
    }
    spans.add(TextSpan(text: text.substring(cursor)));
    return TextSpan(style: style, children: spans);
  }
}
