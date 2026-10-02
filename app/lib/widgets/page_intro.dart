import 'package:flutter/material.dart';

import '../theme/app_theme.dart';
import 'state_message.dart' show isCompactWidth;

/// One sentence of context under a screen's title, plus an optional row
/// of at-a-glance counts ("3 online · 1 disconnected"). Sits directly
/// under the AppBar so every top-level screen opens the same way: title,
/// what this is, then the content. [action] is for tabbed screens, whose
/// shared AppBar can't hold one tab's primary action (see
/// [PrimaryAction.inline]).
class PageIntro extends StatelessWidget {
  final String description;
  final List<Widget> summary;
  final Widget? action;

  const PageIntro({
    super.key,
    required this.description,
    this.summary = const [],
    this.action,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Padding(
      padding: const EdgeInsets.fromLTRB(Space.lg, Space.md, Space.lg, 0),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Expanded(
                child: Text(
                  description,
                  style: theme.textTheme.bodyMedium?.copyWith(
                    color: theme.colorScheme.onSurfaceVariant,
                  ),
                ),
              ),
              if (action != null) ...[const SizedBox(width: Space.lg), action!],
            ],
          ),
          if (summary.isNotEmpty) ...[
            const SizedBox(height: Space.md),
            Wrap(spacing: Space.sm, runSpacing: Space.sm, children: summary),
          ],
        ],
      ),
    );
  }
}

/// A compact "count + label" tile for [PageIntro.summary], with an
/// optional status color on the count.
class SummaryStat extends StatelessWidget {
  final String value;
  final String label;
  final Color? color;

  const SummaryStat({
    super.key,
    required this.value,
    required this.label,
    this.color,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Container(
      padding: const EdgeInsets.symmetric(
        horizontal: Space.md,
        vertical: Space.sm,
      ),
      decoration: BoxDecoration(
        color: theme.colorScheme.surfaceContainer,
        borderRadius: BorderRadius.circular(Radii.sm + 2),
        border: Border.all(color: theme.colorScheme.outlineVariant),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.baseline,
        textBaseline: TextBaseline.alphabetic,
        children: [
          Text(
            value,
            style: theme.textTheme.titleMedium?.copyWith(
              color: color ?? theme.colorScheme.onSurface,
              fontFeatures: const [FontFeature.tabularFigures()],
            ),
          ),
          const SizedBox(width: 6),
          Text(label, style: theme.textTheme.bodySmall),
        ],
      ),
    );
  }
}

/// The screen's one primary action, placed in the AppBar on wide screens
/// (a filled teal button) and as a FAB on phones, where the thumb is.
/// Use [appBarAction] in `AppBar.actions` and [fab] for
/// `Scaffold.floatingActionButton`; each returns null in the layout
/// where the other one is shown.
class PrimaryAction {
  final String label;
  final IconData icon;
  final VoidCallback? onPressed;

  const PrimaryAction({
    required this.label,
    required this.icon,
    required this.onPressed,
  });

  Widget? appBarAction(BuildContext context) {
    if (isCompactWidth(context)) return null;
    return Padding(
      padding: const EdgeInsets.only(right: Space.md, left: Space.xs),
      child: FilledButton.icon(
        onPressed: onPressed,
        icon: Icon(icon, size: 18),
        label: Text(label),
      ),
    );
  }

  /// Like [appBarAction] but without AppBar padding, for [PageIntro.action].
  Widget? inline(BuildContext context) {
    if (isCompactWidth(context)) return null;
    return FilledButton.icon(
      onPressed: onPressed,
      icon: Icon(icon, size: 18),
      label: Text(label),
    );
  }

  Widget? fab(BuildContext context) {
    if (!isCompactWidth(context)) return null;
    return FloatingActionButton(
      onPressed: onPressed,
      tooltip: label,
      child: Icon(icon),
    );
  }
}
