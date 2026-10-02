import 'package:flutter/material.dart';

/// The shared pocket-and-containers brand mark.
class AppLogo extends StatelessWidget {
  final double size;

  const AppLogo({super.key, this.size = 40});

  @override
  Widget build(BuildContext context) => Image.asset(
    'assets/branding/logo.png',
    width: size,
    height: size,
    fit: BoxFit.contain,
    semanticLabel: 'PS-pocketEdge logo',
  );
}
