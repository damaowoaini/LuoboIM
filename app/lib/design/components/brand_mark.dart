import 'package:flutter/material.dart';

import '../tokens.dart';

class BrandMark extends StatelessWidget {
  const BrandMark({super.key, this.size = 64});
  final double size;
  @override
  Widget build(BuildContext context) => ExcludeSemantics(
    child: Container(
      width: size,
      height: size,
      decoration: BoxDecoration(
        color: AppColors.primary,
        borderRadius: BorderRadius.circular(size * .24),
      ),
      child: Icon(Icons.forum_rounded, color: Colors.white, size: size * .52),
    ),
  );
}
