// Drives the real app (macOS) against a running control plane and agent,
// through the Deployment Management screens, saving a screenshot of each
// step. Run with:
//
//   flutter test integration_test/deployment_management_ui_test.dart -d macos \
//     --dart-define=CONTROL_PLANE_URL=http://localhost:18080 \
//     --dart-define=E2E_EMAIL=admin@e2e.test \
//     --dart-define=E2E_PASSWORD=...
//
// Expects seeded data: a healthy "shop-stack" development deployment, a
// production promotion of it awaiting approval, and a Git repository.
import 'dart:io';
import 'dart:ui' as ui;

import 'package:app/main.dart';
import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';

const _email = String.fromEnvironment('E2E_EMAIL');
const _password = String.fromEnvironment('E2E_PASSWORD');
// The macOS app is sandboxed, so screenshots go to its own temp directory
// (printed as they're written) unless SCREENSHOT_DIR says otherwise.
const _shotDirOverride = String.fromEnvironment('SCREENSHOT_DIR');
String get _shotDir =>
    _shotDirOverride.isEmpty ? Directory.systemTemp.path : _shotDirOverride;

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();

  var shotIndex = 0;

  /// Lets real time pass (HTTP, WebSocket) while pumping frames.
  Future<void> wait(WidgetTester tester, [int millis = 1500]) async {
    final end = DateTime.now().add(Duration(milliseconds: millis));
    while (DateTime.now().isBefore(end)) {
      await Future<void>.delayed(const Duration(milliseconds: 100));
      await tester.pump();
    }
  }

  /// Waits until [finder] matches, up to [seconds].
  Future<void> waitFor(
    WidgetTester tester,
    Finder finder, {
    int seconds = 20,
  }) async {
    final end = DateTime.now().add(Duration(seconds: seconds));
    while (DateTime.now().isBefore(end)) {
      await tester.pump();
      if (finder.evaluate().isNotEmpty) return;
      await Future<void>.delayed(const Duration(milliseconds: 200));
    }
    throw TestFailure('Timed out waiting for $finder');
  }

  Future<void> shot(WidgetTester tester, String name) async {
    await tester.pump();
    final view = tester.binding.renderViews.first;
    final layer = view.debugLayer! as OffsetLayer;
    final image = await layer.toImage(
      Offset.zero & (view.size),
      pixelRatio: view.flutterView.devicePixelRatio,
    );
    final bytes = await image.toByteData(format: ui.ImageByteFormat.png);
    final file = File(
      '$_shotDir/${(++shotIndex).toString().padLeft(2, '0')}_$name.png',
    );
    await file.writeAsBytes(bytes!.buffer.asUint8List());
    // ignore: avoid_print
    print('SCREENSHOT ${file.path}');
  }

  testWidgets('deployment management UI sanity check', (tester) async {
    // The macOS debug build has no Keychain entitlement, so the real
    // secure-storage plugin throws; an in-memory stand-in keeps the login
    // flow itself real.
    tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
      const MethodChannel('plugins.it_nomads.com/flutter_secure_storage'),
      (call) async => null,
    );
    // Known, pre-existing: AppShell keeps every module (servers,
    // containers, images, ...) in an IndexedStack, and their
    // FloatingActionButtons share the default hero tag, so any route push
    // logs a duplicate-hero assertion in debug builds. Ignore just that one.
    final originalOnError = FlutterError.onError;
    FlutterError.onError = (details) {
      if (details.exceptionAsString().contains(
        'multiple heroes that share the same tag',
      )) {
        return;
      }
      originalOnError?.call(details);
    };
    addTearDown(() => FlutterError.onError = originalOnError);
    await tester.binding.setSurfaceSize(const Size(1400, 900));
    await tester.pumpWidget(const PSPocketEdgeApp());
    await wait(tester, 2000);

    // A session restored from a previous run may belong to another
    // control plane — start from a clean login.
    if (find.text('Log in').evaluate().isEmpty) {
      await tester.tap(find.byTooltip('Account'));
      await wait(tester, 500);
      await tester.tap(find.text('Log out').last);
      await wait(tester, 1000);
    }
    await tester.enterText(find.widgetWithText(TextField, 'Email'), _email);
    await tester.enterText(
      find.widgetWithText(TextField, 'Password'),
      _password,
    );
    await tester.tap(find.text('Log in'));
    await waitFor(tester, find.text('Deployment Management'));
    await wait(tester);

    // Compose files tab.
    await tester.tap(find.text('Deployment Management'));
    await waitFor(tester, find.text('shop-stack'));
    await wait(tester, 800);
    await shot(tester, 'compose_files');

    // Deployment history.
    await tester.tap(find.text('Stack deployment'));
    await waitFor(tester, find.text('Verified healthy'));
    await wait(tester, 800);
    await shot(tester, 'deployment_history');

    // Open the healthy development deployment.
    await tester.tap(find.text('development').first);
    await waitFor(tester, find.text('Timeline'));
    await waitFor(tester, find.textContaining('Rolling updates'));
    await wait(tester, 2500);
    await shot(tester, 'status_timeline');

    await tester.tap(find.text('Services'));
    await wait(tester, 1000);
    await shot(tester, 'status_services');

    await tester.tap(find.text('Revisions'));
    await waitFor(tester, find.textContaining('(current)'));
    await wait(tester, 500);
    await shot(tester, 'status_revisions');

    await tester.tap(find.text('Drift'));
    await wait(tester, 500);
    await tester.tap(find.text('Check for drift'));
    await waitFor(tester, find.text('Running containers'));
    await wait(tester, 500);
    await shot(tester, 'status_drift');

    // Stack menu and rollback menu (cancelled — just checking they open).
    await tester.tap(find.widgetWithText(OutlinedButton, 'Stack'));
    await wait(tester, 500);
    await shot(tester, 'status_stack_menu');
    await tester.sendKeyEvent(LogicalKeyboardKey.escape);
    await wait(tester, 500);
    await tester.tap(find.widgetWithText(OutlinedButton, 'Roll back'));
    await wait(tester, 800);
    await shot(tester, 'rollback_menu');
    await tester.tapAt(const Offset(10, 10));
    await wait(tester, 500);

    // Scale dialog (cancelled).
    await tester.tap(find.text('Services'));
    await wait(tester, 500);
    await tester.tap(find.widgetWithText(TextButton, 'Scale').first);
    await wait(tester, 500);
    await shot(tester, 'scale_dialog');
    await tester.tap(find.text('Cancel'));
    await wait(tester, 500);

    // Deployment settings dialog (cancelled).
    await tester.tap(
      find.byTooltip('Environment, change request, rollout settings'),
    );
    await wait(tester, 800);
    await shot(tester, 'settings_dialog');
    await tester.tap(find.text('Cancel'));
    await wait(tester, 500);

    await tester.pageBack();
    await wait(tester, 1000);

    // Git-based deployment.
    await tester.tap(find.text('Git-based deployment'));
    await waitFor(tester, find.text('awesome-compose'));
    await wait(tester, 800);
    await shot(tester, 'git_repositories');
    await tester.tap(find.text('Import Compose file'));
    await waitFor(tester, find.textContaining('(branch)'), seconds: 60);
    await wait(tester, 500);
    await shot(tester, 'git_import_dialog');
    await tester.tap(find.text('Cancel'));
    await wait(tester, 500);

    // Governance: approve the pending production promotion.
    await tester.tap(find.text('Governance'));
    await waitFor(tester, find.text('Approve'));
    await wait(tester, 800);
    await shot(tester, 'governance_approvals');
    await tester.tap(find.widgetWithText(FilledButton, 'Approve'));
    await wait(tester, 800);
    await shot(tester, 'approve_dialog');
    await tester.tap(find.widgetWithText(FilledButton, 'Approve').last);
    await waitFor(tester, find.textContaining('Recently decided'));
    await wait(tester, 2000);
    await shot(tester, 'governance_after_approve');

    await tester.tap(find.text('Environments'));
    await waitFor(tester, find.text('Production'));
    await wait(tester, 800);
    await shot(tester, 'governance_environments');
    await tester.tap(find.byTooltip('Edit policy').last);
    await wait(tester, 800);
    await shot(tester, 'policy_dialog');
    await tester.tap(find.text('Cancel'));
    await wait(tester, 500);

    await tester.tap(find.text('Audit trail'));
    await waitFor(tester, find.textContaining('approved'));
    await wait(tester, 800);
    await shot(tester, 'governance_audit');

    // The promoted production deployment, now deploying/deployed.
    await tester.tap(find.text('Stack deployment'));
    await wait(tester, 1500);
    await tester.tap(find.byTooltip('Refresh').first);
    await waitFor(tester, find.text('production'));
    await tester.tap(find.text('production').first);
    await waitFor(tester, find.text('Timeline'));
    await waitFor(tester, find.text('Verified healthy'), seconds: 90);
    await wait(tester, 1500);
    await shot(tester, 'production_status');
  });
}
