// Copy this file into the OLD app, retaining its original application ID and
// signing identity. A different app cannot read the old SharedPreferences.
import 'dart:convert';
import 'dart:io';
import 'dart:math';

import 'package:flutter/material.dart';
import 'package:path_provider/path_provider.dart';
import 'package:share_plus/share_plus.dart';
import 'package:shared_preferences/shared_preferences.dart';

/// Preserve the exact original strings, including malformed JSON, so exporting
/// never deletes the only recoverable copy. No account ownership is invented.
List<int> legacyPersonalBackupBytes({String? rawNotes, String? rawTodos}) =>
    utf8.encode(
      jsonEncode({
        'sourceFormat': 'legacy-device-local-v1',
        'exportedAt': DateTime.now().toUtc().toIso8601String(),
        'local_notes': rawNotes ?? '[]',
        'local_todos': rawTodos ?? '[]',
      }),
    );

class LegacyPersonalExportButton extends StatefulWidget {
  const LegacyPersonalExportButton({super.key});
  @override
  State<LegacyPersonalExportButton> createState() =>
      _LegacyPersonalExportButtonState();
}

class _LegacyPersonalExportButtonState
    extends State<LegacyPersonalExportButton> {
  bool busy = false;
  Future<void> export() async {
    setState(() => busy = true);
    try {
      final preferences = await SharedPreferences.getInstance();
      final bytes = legacyPersonalBackupBytes(
        rawNotes: preferences.getString('local_notes'),
        rawTodos: preferences.getString('local_todos'),
      );
      final directory = await Directory(
        '${(await getApplicationDocumentsDirectory()).path}/personal-backups',
      ).create(recursive: true);
      final file = File(
        '${directory.path}/legacy-personal-${DateTime.now().microsecondsSinceEpoch}-${Random.secure().nextInt(1 << 32)}.json',
      );
      await file.writeAsBytes(bytes, flush: true);
      if (!mounted) return;
      final box = context.findRenderObject() as RenderBox?;
      // This API also exists in the old app's share_plus 7.2.x. It intentionally
      // remains compatible with that app instead of requiring a framework upgrade.
      // ignore: deprecated_member_use
      await Share.shareXFiles(
        [XFile(file.path, mimeType: 'application/json')],
        subject: '笔记与待办备份',
        sharePositionOrigin: box == null
            ? null
            : box.localToGlobal(Offset.zero) & box.size,
      );
    } catch (_) {
      if (mounted)
        ScaffoldMessenger.of(context)
            .showSnackBar(const SnackBar(content: Text('导出未完成，原资料仍保留，请重试')));
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  @override
  Widget build(BuildContext context) => OutlinedButton.icon(
    onPressed: busy ? null : export,
    icon: const Icon(Icons.file_upload_outlined),
    label: Text(busy ? '正在导出' : '导出笔记与待办'),
  );
}
