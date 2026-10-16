import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';

import '../domain/chat_mention.dart';

/// Received text is read-only, so replacing wire @UID with a display name never
/// changes EditableText selection/composing positions.
class MentionedMessageText extends StatefulWidget {
  const MentionedMessageText({
    super.key,
    required this.text,
    required this.mentions,
    required this.onMention,
    this.canOpen,
  });
  final String text;
  final List<ReceivedMention> mentions;
  final void Function(MentionIdentity) onMention;
  final bool Function(MentionIdentity)? canOpen;
  @override
  State<MentionedMessageText> createState() => _MentionedMessageTextState();
}

class _MentionedMessageTextState extends State<MentionedMessageText> {
  final recognizers = <TapGestureRecognizer>[];
  void clear() {
    for (final recognizer in recognizers) {
      recognizer.dispose();
    }
    recognizers.clear();
  }

  @override
  void dispose() {
    clear();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    clear();
    final spans = <InlineSpan>[];
    var cursor = 0;
    for (final mention in widget.mentions) {
      if (mention.start < cursor ||
          mention.end > widget.text.length ||
          !mention.identity.valid) {
        continue;
      }
      spans.add(TextSpan(text: widget.text.substring(cursor, mention.start)));
      final open = widget.canOpen?.call(mention.identity) ?? true;
      final recognizer = open
          ? (TapGestureRecognizer()
              ..onTap = () => widget.onMention(mention.identity))
          : null;
      if (recognizer != null) recognizers.add(recognizer);
      spans.add(
        TextSpan(
          text: mention.identity.label,
          semanticsLabel:
              '${mention.identity.label}，${mention.identity.identityLabel}',
          style: TextStyle(
            color: open ? Theme.of(context).colorScheme.primary : null,
            decoration: open ? TextDecoration.underline : null,
          ),
          recognizer: recognizer,
        ),
      );
      cursor = mention.end;
    }
    spans.add(TextSpan(text: widget.text.substring(cursor)));
    return Text.rich(
      TextSpan(children: spans),
      style: const TextStyle(fontSize: 16, height: 1.45),
    );
  }
}
