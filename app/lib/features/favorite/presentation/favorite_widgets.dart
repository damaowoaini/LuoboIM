import 'package:flutter/material.dart';

import '../../../design/tokens.dart';
import '../domain/favorite.dart';

IconData favoriteIcon(FavoriteKind kind) => switch (kind) {
  FavoriteKind.message => Icons.chat_bubble_outline,
  FavoriteKind.file => Icons.insert_drive_file_outlined,
  FavoriteKind.image => Icons.image_outlined,
  FavoriteKind.video => Icons.videocam_outlined,
  FavoriteKind.audio => Icons.mic_none,
  FavoriteKind.link => Icons.link,
};
Color favoriteColor(String hex) => RegExp(r'^#[0-9a-fA-F]{6}$').hasMatch(hex)
    ? Color(0xff000000 | int.parse(hex.substring(1), radix: 16))
    : AppColors.primary;
String favoriteDate(int milliseconds) {
  if (milliseconds <= 0) return '未记录';
  final date = DateTime.fromMillisecondsSinceEpoch(milliseconds).toLocal();
  String two(int value) => value.toString().padLeft(2, '0');
  return '${date.year}/${two(date.month)}/${two(date.day)} ${two(date.hour)}:${two(date.minute)}';
}

List<String> favoriteTags(String text) => text
    .split(RegExp(r'[,，\n]'))
    .map((value) => value.trim())
    .where((value) => value.isNotEmpty)
    .toSet()
    .toList();

Future<String?> chooseFavoriteCategory(
  BuildContext context,
  List<FavoriteCategory> categories,
) => showModalBottomSheet<String>(
  context: context,
  builder: (context) => SafeArea(
    child: ListView(
      shrinkWrap: true,
      children: [
        const ListTile(
          title: Text('选择分类', style: TextStyle(fontWeight: FontWeight.w600)),
        ),
        for (final category in categories)
          ListTile(
            leading: Icon(
              Icons.folder_outlined,
              color: favoriteColor(category.color),
            ),
            title: Text(category.name),
            trailing: Text('${category.count}'),
            onTap: () => Navigator.pop(context, category.id),
          ),
      ],
    ),
  ),
);

class FavoriteTile extends StatelessWidget {
  const FavoriteTile({
    super.key,
    required this.item,
    required this.onTap,
    this.category,
    this.selected,
    this.onLongPress,
  });
  final FavoriteItem item;
  final FavoriteCategory? category;
  final bool? selected;
  final VoidCallback onTap;
  final VoidCallback? onLongPress;
  @override
  Widget build(BuildContext context) => Card(
    elevation: 0,
    child: ListTile(
      contentPadding: const EdgeInsets.all(16),
      leading: CircleAvatar(
        backgroundColor: const Color(0xffe6efff),
        child: Icon(favoriteIcon(item.kind), color: AppColors.primary),
      ),
      title: Text(
        item.displayTitle,
        maxLines: 2,
        overflow: TextOverflow.ellipsis,
        style: const TextStyle(fontWeight: FontWeight.w600),
      ),
      subtitle: Padding(
        padding: const EdgeInsets.only(top: 8),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            if (item.canUseContent && item.decoded.summary != item.title)
              Text(
                item.decoded.summary,
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
              ),
            const SizedBox(height: 8),
            Wrap(
              spacing: 8,
              runSpacing: 4,
              children: [
                Text(
                  category?.name ?? '默认分类',
                  style: TextStyle(
                    color: favoriteColor(category?.color ?? '#2563EB'),
                  ),
                ),
                Text(
                  favoriteDate(item.createdAt),
                  style: const TextStyle(
                    color: AppColors.secondary,
                    fontSize: 12,
                  ),
                ),
                for (final tag
                    in item.canUseContent
                        ? item.tags.take(3)
                        : const <String>[])
                  Text(
                    '#$tag',
                    style: const TextStyle(
                      color: AppColors.secondary,
                      fontSize: 12,
                    ),
                  ),
              ],
            ),
          ],
        ),
      ),
      trailing: selected == null
          ? const Icon(Icons.chevron_right)
          : Checkbox(value: selected, onChanged: (_) => onTap()),
      onTap: onTap,
      onLongPress: onLongPress,
    ),
  );
}
