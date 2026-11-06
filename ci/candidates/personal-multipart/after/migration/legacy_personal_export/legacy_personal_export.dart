// Copy this file into the OLD app, retaining its original application ID and
// signing identity. A different app cannot read the old SharedPreferences.
import 'dart:convert';
import 'dart:io';
import 'dart:math';

import 'package:crypto/crypto.dart';
import 'package:flutter/material.dart';
import 'package:path_provider/path_provider.dart';
import 'package:share_plus/share_plus.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'personal_migration.dart';

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

/// Only the OLD app adapter touches its filesystem. The shared codec remains
/// portable. The stream must fully finish and fsync before its proof is returned.
Future<PersonalMigrationFile> writeLegacyPersonalMigrationFile(
  Directory directory, String name, Stream<List<int>> chunks,
) async {
  if (!RegExp(r'^(original|manifest|unmigrated|part-[0-9]+)\.json$').hasMatch(name)) {
    throw const FormatException('Invalid backup filename');
  }
  final file = File('${directory.path}/$name');
  if (await file.exists()) throw const FileSystemException('Backup exists');
  final sink = file.openWrite();
  try { await sink.addStream(chunks); await sink.flush(); }
  finally { await sink.close(); }
  final handle = await file.open(mode: FileMode.append);
  try { await handle.flush(); } finally { await handle.close(); }
  final sha = (await sha256.bind(file.openRead()).first).toString();
  final proof = PersonalMigrationFile(await file.length(), sha);
  if (name == 'original.json') {
    await File('${directory.path}/raw-complete').writeAsString(sha, flush: true);
  }
  return proof;
}

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
      final directory = await Directory(
        '${(await getApplicationDocumentsDirectory()).path}/personal-backups/legacy-${DateTime.now().microsecondsSinceEpoch}-${Random.secure().nextInt(1 << 32)}',
      ).create(recursive: true);
      List<XFile> files;
      var rawOnly = false;
      var incomplete = false;
      try {
        final manifest = await exportLegacyPersonalMigration(
          write: (name, chunks) => writeLegacyPersonalMigrationFile(directory, name, chunks),
          rawNotes: preferences.getString('local_notes'),
          rawTodos: preferences.getString('local_todos'));
        incomplete = manifest.rejected > 0 || manifest.incomplete;
        files = [XFile('${directory.path}/manifest.json', mimeType: 'application/json'),
          for (final name in ['original.json', 'unmigrated.json',
              ...manifest.parts.map((part) => part['file'] as String)])
            XFile('${directory.path}/$name', mimeType: 'application/json')];
      } catch (_) {
        // A conversion error does not hide an already completed raw backup.
        final raw = File('${directory.path}/original.json');
        if (!await raw.exists() || !await File('${directory.path}/raw-complete').exists()) rethrow;
        files = [XFile(raw.path, mimeType: 'application/json')];
        rawOnly = true;
      }
      if (!mounted) return;
      final box = context.findRenderObject() as RenderBox?;
      // This API also exists in the old app's share_plus 7.2.x. It intentionally
      // remains compatible with that app instead of requiring a framework upgrade.
      // ignore: deprecated_member_use
      await Share.shareXFiles(
        files,
        subject: '笔记与待办备份',
        sharePositionOrigin: box == null
            ? null
            : box.localToGlobal(Offset.zero) & box.size,
      );
      if (mounted && (rawOnly || incomplete)) {
        ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(
          rawOnly ? '原始备份已保留，但分卷未完成；不要删除旧资料' :
            '部分记录未迁移，原文保留在原始备份；请同时保存未迁移报告')));
      }
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
