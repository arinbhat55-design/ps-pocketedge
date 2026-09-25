import 'package:flutter/material.dart';

/// The app's fixed palette: charcoal surfaces, teal reserved for primary
/// actions and selection, and one green/amber/red set used for every
/// healthy/warning/failed signal. Defined explicitly (not generated from a
/// seed) so each role means one thing everywhere.
abstract final class AppColors {
  // Surfaces, darkest to lightest. Charcoal, not near-black.
  static const background = Color(0xFF1A1D21);
  static const surfaceLow = Color(0xFF1F2327);
  static const surface = Color(0xFF23282D);
  static const surfaceHigh = Color(0xFF2A2F35);
  static const surfaceHighest = Color(0xFF31373E);

  static const border = Color(0xFF343B43);
  static const borderStrong = Color(0xFF4A535D);

  static const textPrimary = Color(0xFFE6EAEE);
  static const textSecondary = Color(0xFFA2ACB7);
  static const textMuted = Color(0xFF737D88);

  // Accent: primary actions and selection only.
  static const teal = Color(0xFF34B8A6);
  static const onTeal = Color(0xFF03211D);
  static const tealContainer = Color(0xFF16423C);
  static const onTealContainer = Color(0xFFA5EFE3);

  // Status. Always paired with a text label (see StatusPill).
  static const healthy = Color(0xFF4CC26A);
  static const warning = Color(0xFFE0A93B);
  static const failed = Color(0xFFF0625A);
  static const neutral = Color(0xFF8D98A5);

  // Non-status action accent (restart, logs): distinct from teal so a row
  // of actions is easy to tell apart at a glance.
  static const info = Color(0xFF6AA8F0);

  // Chart line when a metric is in its normal range: quiet, so the eye
  // goes to the value above it and only warning/failed ranges pop.
  static const chartLine = Color(0xFF9FB3C8);
}

/// Spacing scale. Filters, rows, cards and section headings all step
/// through these values so related things line up.
abstract final class Space {
  static const double xs = 4;
  static const double sm = 8;
  static const double md = 12;
  static const double lg = 16;
  static const double xl = 24;
  static const double xxl = 32;
}

abstract final class Radii {
  static const double sm = 6;
  static const double md = 10;
  static const double lg = 14;
}

/// Text styles that aren't in [TextTheme].
abstract final class AppText {
  /// IDs, hashes, ports, paths and other technical detail: smaller,
  /// monospace, secondary color, so names and values stay dominant.
  static TextStyle mono(BuildContext context, {double size = 12}) {
    return TextStyle(
      fontFamily: 'monospace',
      fontFamilyFallback: const ['Menlo', 'Consolas', 'Roboto Mono', 'Courier'],
      fontSize: size,
      height: 1.4,
      color: Theme.of(context).colorScheme.onSurfaceVariant,
    );
  }
}

abstract final class AppTheme {
  static ThemeData dark() {
    const scheme = ColorScheme(
      brightness: Brightness.dark,
      primary: AppColors.teal,
      onPrimary: AppColors.onTeal,
      primaryContainer: AppColors.tealContainer,
      onPrimaryContainer: AppColors.onTealContainer,
      // Secondary is deliberately neutral slate, not a second accent:
      // Material uses it for tonal buttons, so those stay quiet and teal
      // keeps meaning "the primary action / the current selection".
      secondary: Color(0xFFB4BEC9),
      onSecondary: Color(0xFF1B2127),
      secondaryContainer: AppColors.surfaceHighest,
      onSecondaryContainer: AppColors.textPrimary,
      tertiary: Color(0xFF8FB4D9),
      onTertiary: Color(0xFF0F2336),
      tertiaryContainer: Color(0xFF233547),
      onTertiaryContainer: Color(0xFFCFE2F5),
      error: AppColors.failed,
      onError: Color(0xFF2D0806),
      errorContainer: Color(0xFF4A1F1C),
      onErrorContainer: Color(0xFFFFD9D5),
      surface: AppColors.background,
      onSurface: AppColors.textPrimary,
      onSurfaceVariant: AppColors.textSecondary,
      surfaceContainerLowest: Color(0xFF16191C),
      surfaceContainerLow: AppColors.surfaceLow,
      surfaceContainer: AppColors.surface,
      surfaceContainerHigh: AppColors.surfaceHigh,
      surfaceContainerHighest: AppColors.surfaceHighest,
      outline: AppColors.borderStrong,
      outlineVariant: AppColors.border,
      shadow: Colors.black,
      scrim: Colors.black,
      inverseSurface: AppColors.textPrimary,
      onInverseSurface: AppColors.background,
      inversePrimary: Color(0xFF00695C),
      surfaceTint: Colors.transparent,
    );
    return _build(scheme);
  }

  /// Kept for completeness (the app forces dark mode); same component
  /// styling over a seed-generated light scheme with the same teal accent.
  static ThemeData light() {
    final scheme = ColorScheme.fromSeed(
      seedColor: AppColors.teal,
      surfaceTint: Colors.transparent,
    );
    return _build(scheme);
  }

  static ThemeData _build(ColorScheme scheme) {
    final base = ThemeData(colorScheme: scheme, useMaterial3: true);
    // apply() first: it overwrites every style's color, so the per-style
    // colors below have to come after it.
    final t = base.textTheme.apply(
      bodyColor: scheme.onSurface,
      displayColor: scheme.onSurface,
    );
    final text = t.copyWith(
      titleLarge: t.titleLarge?.copyWith(
        fontSize: 20,
        fontWeight: FontWeight.w600,
        letterSpacing: 0,
      ),
      titleMedium: t.titleMedium?.copyWith(fontWeight: FontWeight.w600),
      titleSmall: t.titleSmall?.copyWith(fontWeight: FontWeight.w600),
      labelLarge: t.labelLarge?.copyWith(fontWeight: FontWeight.w600),
      bodySmall: t.bodySmall?.copyWith(color: scheme.onSurfaceVariant),
    );

    final cardShape = RoundedRectangleBorder(
      borderRadius: BorderRadius.circular(Radii.md),
      side: BorderSide(color: scheme.outlineVariant),
    );
    final inputBorder = OutlineInputBorder(
      borderRadius: BorderRadius.circular(Radii.sm + 2),
      borderSide: BorderSide(color: scheme.outlineVariant),
    );

    return base.copyWith(
      textTheme: text,
      scaffoldBackgroundColor: scheme.surface,
      visualDensity: VisualDensity.standard,
      appBarTheme: AppBarTheme(
        backgroundColor: scheme.surface,
        foregroundColor: scheme.onSurface,
        surfaceTintColor: Colors.transparent,
        elevation: 0,
        scrolledUnderElevation: 0,
        centerTitle: false,
        titleTextStyle: text.titleLarge,
        shape: Border(bottom: BorderSide(color: scheme.outlineVariant)),
      ),
      cardTheme: CardThemeData(
        color: scheme.surfaceContainer,
        surfaceTintColor: Colors.transparent,
        elevation: 0,
        shape: cardShape,
      ),
      dividerTheme: DividerThemeData(
        color: scheme.outlineVariant,
        thickness: 1,
        space: 1,
      ),
      listTileTheme: ListTileThemeData(
        iconColor: scheme.onSurfaceVariant,
        contentPadding: const EdgeInsets.symmetric(horizontal: Space.lg),
      ),
      navigationRailTheme: NavigationRailThemeData(
        backgroundColor: scheme.surfaceContainerLow,
        indicatorColor: scheme.primaryContainer,
        selectedIconTheme: IconThemeData(color: scheme.onPrimaryContainer),
        unselectedIconTheme: IconThemeData(color: scheme.onSurfaceVariant),
        selectedLabelTextStyle: text.labelLarge?.copyWith(
          color: scheme.primary,
        ),
        unselectedLabelTextStyle: text.labelLarge?.copyWith(
          color: scheme.onSurfaceVariant,
          fontWeight: FontWeight.w500,
        ),
      ),
      navigationBarTheme: NavigationBarThemeData(
        backgroundColor: scheme.surfaceContainerLow,
        indicatorColor: scheme.primaryContainer,
        surfaceTintColor: Colors.transparent,
        elevation: 0,
        height: 68,
        iconTheme: WidgetStateProperty.resolveWith(
          (states) => IconThemeData(
            color: states.contains(WidgetState.selected)
                ? scheme.onPrimaryContainer
                : scheme.onSurfaceVariant,
          ),
        ),
        labelTextStyle: WidgetStateProperty.resolveWith(
          (states) => text.labelMedium?.copyWith(
            color: states.contains(WidgetState.selected)
                ? scheme.primary
                : scheme.onSurfaceVariant,
            fontWeight: states.contains(WidgetState.selected)
                ? FontWeight.w600
                : FontWeight.w500,
          ),
        ),
      ),
      tabBarTheme: TabBarThemeData(
        labelColor: scheme.primary,
        unselectedLabelColor: scheme.onSurfaceVariant,
        indicatorColor: scheme.primary,
        dividerColor: scheme.outlineVariant,
        labelStyle: text.titleSmall,
        unselectedLabelStyle: text.titleSmall?.copyWith(
          fontWeight: FontWeight.w500,
        ),
      ),
      dataTableTheme: DataTableThemeData(
        headingRowColor: WidgetStatePropertyAll(scheme.surfaceContainerLow),
        headingRowHeight: 40,
        dataRowMinHeight: 44,
        dataRowMaxHeight: 52,
        horizontalMargin: Space.lg,
        columnSpacing: Space.xl,
        dividerThickness: 1,
        headingTextStyle: text.labelMedium?.copyWith(
          color: scheme.onSurfaceVariant,
          fontWeight: FontWeight.w600,
          letterSpacing: 0.4,
        ),
        dataTextStyle: text.bodyMedium,
      ),
      inputDecorationTheme: InputDecorationTheme(
        filled: true,
        fillColor: scheme.surfaceContainerLow,
        border: inputBorder,
        enabledBorder: inputBorder,
        focusedBorder: inputBorder.copyWith(
          borderSide: BorderSide(color: scheme.primary, width: 1.5),
        ),
        hintStyle: text.bodyMedium?.copyWith(color: AppColors.textMuted),
      ),
      chipTheme: ChipThemeData(
        backgroundColor: scheme.surfaceContainerLow,
        selectedColor: scheme.primaryContainer,
        side: BorderSide(color: scheme.outlineVariant),
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(Radii.sm + 2),
        ),
        labelStyle: text.labelLarge?.copyWith(fontWeight: FontWeight.w500),
        showCheckmark: false,
      ),
      floatingActionButtonTheme: FloatingActionButtonThemeData(
        backgroundColor: scheme.primary,
        foregroundColor: scheme.onPrimary,
        elevation: 2,
        highlightElevation: 4,
      ),
      dialogTheme: DialogThemeData(
        backgroundColor: scheme.surfaceContainerHigh,
        surfaceTintColor: Colors.transparent,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(Radii.lg),
          side: BorderSide(color: scheme.outlineVariant),
        ),
      ),
      popupMenuTheme: PopupMenuThemeData(
        color: scheme.surfaceContainerHigh,
        surfaceTintColor: Colors.transparent,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(Radii.md),
          side: BorderSide(color: scheme.outlineVariant),
        ),
      ),
      bottomSheetTheme: BottomSheetThemeData(
        backgroundColor: scheme.surfaceContainerLow,
        surfaceTintColor: Colors.transparent,
      ),
      snackBarTheme: SnackBarThemeData(
        behavior: SnackBarBehavior.floating,
        backgroundColor: scheme.surfaceContainerHighest,
        contentTextStyle: text.bodyMedium,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(Radii.md),
        ),
      ),
      expansionTileTheme: ExpansionTileThemeData(
        shape: const Border(),
        collapsedShape: const Border(),
        iconColor: scheme.onSurfaceVariant,
        collapsedIconColor: scheme.onSurfaceVariant,
      ),
      segmentedButtonTheme: SegmentedButtonThemeData(
        style: ButtonStyle(
          visualDensity: VisualDensity.compact,
          backgroundColor: WidgetStateProperty.resolveWith(
            (states) => states.contains(WidgetState.selected)
                ? scheme.primaryContainer
                : Colors.transparent,
          ),
          foregroundColor: WidgetStateProperty.resolveWith(
            (states) => states.contains(WidgetState.selected)
                ? scheme.onPrimaryContainer
                : scheme.onSurfaceVariant,
          ),
          side: WidgetStatePropertyAll(
            BorderSide(color: scheme.outlineVariant),
          ),
        ),
      ),
      progressIndicatorTheme: ProgressIndicatorThemeData(
        color: scheme.primary,
        linearTrackColor: scheme.surfaceContainerHighest,
      ),
    );
  }
}
