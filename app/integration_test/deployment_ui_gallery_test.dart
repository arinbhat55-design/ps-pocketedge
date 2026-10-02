// Visual audit of the Deployment Management UI: captures every screen and
// dialog at a desktop and a narrow window size, and reports (rather than
// fails on) layout overflows, so layout problems can be reviewed from the
// screenshots. Same prerequisites as deployment_management_ui_test.dart.
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

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();

  final problems = <String>[];

  Future<void> wait(WidgetTester tester, [int millis = 1200]) async {
    final end = DateTime.now().add(Duration(milliseconds: millis));
    while (DateTime.now().isBefore(end)) {
      await Future<void>.delayed(const Duration(milliseconds: 100));
      await tester.pump();
    }
  }

  Future<void> waitFor(
    WidgetTester tester,
    Finder f, {
    int seconds = 30,
  }) async {
    final end = DateTime.now().add(Duration(seconds: seconds));
    while (DateTime.now().isBefore(end)) {
      await tester.pump();
      if (f.evaluate().isNotEmpty) return;
      await Future<void>.delayed(const Duration(milliseconds: 200));
    }
    throw TestFailure('Timed out waiting for $f');
  }

  Future<void> run(WidgetTester tester, String label, Size size) async {
    var index = 0;
    Future<void> shot(String name) async {
      // ignore: avoid_print
      print('STEP $label $name');
      await wait(tester, 600);
      final view = tester.binding.renderViews.first;
      final layer = view.debugLayer! as OffsetLayer;
      // The app is laid out at [size] and scaled to fit the window
      // (FittedBox, top-left), so capture only the area it occupies — the
      // rest of the window is empty and would show as a blank band.
      final scale = [
        view.size.width / size.width,
        view.size.height / size.height,
      ].reduce((a, b) => a < b ? a : b);
      final image = await layer.toImage(
        Offset.zero & (size * scale),
        pixelRatio: 2 / scale,
      );
      final bytes = await image.toByteData(format: ui.ImageByteFormat.png);
      final file = File(
        '${Directory.systemTemp.path}/${label}_${(++index).toString().padLeft(2, '0')}_$name.png',
      );
      await file.writeAsBytes(bytes!.buffer.asUint8List());
    }

    // Runs one audit step; a failure is recorded and the audit moves on
    // (dismissing whatever dialog/route the step left open).
    Future<void> step(String name, Future<void> Function() body) async {
      try {
        await body();
      } catch (e) {
        problems.add(
          'STEP FAILED $label/$name: ${e.toString().split('\n').first}',
        );
        for (var i = 0; i < 3; i++) {
          final nav = find.byType(Navigator);
          if (nav.evaluate().isEmpty) break;
          final state = tester.state<NavigatorState>(nav.first);
          if (!state.canPop()) break;
          state.pop();
          await wait(tester, 400);
        }
      }
    }

    Future<void> close() async {
      final cancel = find.text('Cancel');
      if (cancel.evaluate().isNotEmpty) {
        await tester.tap(cancel.last);
      } else if (find.text('Close').evaluate().isNotEmpty) {
        await tester.tap(find.text('Close').last);
      } else if (find.text('Done').evaluate().isNotEmpty) {
        await tester.tap(find.text('Done').last);
      } else {
        await tester.tapAt(const Offset(4, 4));
      }
      await wait(tester, 600);
    }

    // ignore: avoid_print
    print('STEP $label start');
    // Lay the app out at [size] and scale it into the real window, so a
    // desktop-sized layout can be audited whatever the window size is.
    await tester.pumpWidget(
      FittedBox(
        alignment: Alignment.topLeft,
        child: SizedBox.fromSize(
          size: size,
          child: MediaQuery(
            data: MediaQueryData(size: size, devicePixelRatio: 2),
            child: const PSPocketEdgeApp(),
          ),
        ),
      ),
    );
    await wait(tester, 1500);
    if (find.text('Log in').evaluate().isNotEmpty) {
      await tester.enterText(find.widgetWithText(TextField, 'Email'), _email);
      await tester.enterText(
        find.widgetWithText(TextField, 'Password'),
        _password,
      );
      await tester.tap(find.text('Log in'));
    }
    await waitFor(tester, find.text('Deployment Management'));
    await tester.tap(find.text('Deployment Management'));
    await wait(tester, 800);
    await tester.tap(find.text('Compose files'));
    await waitFor(tester, find.text('shop-stack'));
    await shot('compose_files');

    await step('Deploy flow dialogs.', () async {
      await tester.tap(find.byTooltip('Deploy').last);
      await waitFor(tester, find.byType(SimpleDialog));
      await shot('deploy_pick_server');
      await tester.tap(find.byType(SimpleDialogOption).first);
      await waitFor(tester, find.text('Deployment setup'));
      await shot('deploy_setup');
      await tester.tap(find.text('Continue'));
      await waitFor(tester, find.textContaining('Deploy shop-stack to'));
      await shot('deploy_preview');
      await close();
    });

    await step('Tabs.', () async {
      for (final tab in [
        'Stack deployment',
        'Configuration',
        'Git-based deployment',
        'Pre-deployment validation',
      ]) {
        await tester.ensureVisible(find.text(tab));
        await tester.tap(find.text(tab));
        await wait(tester, 2500);
        await shot(tab.toLowerCase().replaceAll(' ', '_'));
      }
    });
    await step('Git dialogs.', () async {
      await tester.ensureVisible(find.text('Git-based deployment'));
      await tester.tap(find.text('Git-based deployment'));
      await wait(tester, 1500);
      await tester.tap(find.text('Add repository'));
      await wait(tester);
      await shot('git_add_repo');
      await close();
      await tester.tap(find.byType(PopupMenuButton<String>).last);
      await wait(tester, 600);
      await tester.tap(find.text('Webhook setup'));
      await waitFor(tester, find.text('Push webhook'));
      await shot('git_webhook');
      await tester.tap(find.text('Done'));
      await wait(tester, 500);
      await tester.tap(find.text('Import Compose file'));
      await waitFor(tester, find.textContaining('(branch)'), seconds: 60);
      await tester.enterText(
        find.widgetWithText(TextField, 'Path to the Compose file'),
        'nginx-golang/compose.yaml',
      );
      await tester.tap(find.text('Preview'));
      await waitFor(tester, find.textContaining('Commit '), seconds: 60);
      await shot('git_import_preview');
      await close();
    });
    await step('Governance.', () async {
      await tester.ensureVisible(find.text('Governance'));
      await tester.tap(find.text('Governance'));
      await wait(tester, 2000);
      await shot('gov_approvals');
      await tester.tap(find.text('Environments'));
      await wait(tester, 1500);
      await shot('gov_environments');
      await tester.tap(find.byTooltip('Edit policy').at(2));
      await wait(tester);
      await shot('gov_policy_staging');
      await close();
      await tester.tap(find.text('Audit trail'));
      await wait(tester, 1500);
      await shot('gov_audit');
    });
    await step(
      'Deployment status screen (development, 2 revisions).',
      () async {
        await tester.ensureVisible(find.text('Stack deployment'));
        await tester.tap(find.text('Stack deployment'));
        await wait(tester, 1500);
        await tester.tap(find.text('development').first);
        await waitFor(tester, find.text('Timeline'));
        await wait(tester, 2500);
        await shot('status_timeline');
        for (final tab in ['Services', 'Revisions']) {
          await tester.ensureVisible(find.text(tab));
          await tester.tap(find.text(tab));
          await wait(tester, 1500);
          await shot('status_${tab.toLowerCase()}');
        }
        await tester.tap(find.widgetWithText(TextButton, 'View').first);
        await wait(tester);
        await shot('status_revision_yaml');
        await close();
        await tester.tap(find.text('Drift'));
        await wait(tester, 500);
        await tester.tap(find.text('Check for drift'));
        await waitFor(tester, find.text('Running containers'));
        await shot('status_drift');
        await tester.tap(find.widgetWithText(OutlinedButton, 'Stack'));
        await wait(tester, 600);
        await shot('status_stack_menu');
        await tester.sendKeyEvent(LogicalKeyboardKey.escape);
        await wait(tester, 400);
        await tester.tap(find.widgetWithText(OutlinedButton, 'Promote'));
        await waitFor(tester, find.textContaining('Promote revision'));
        await shot('status_promote');
        await close();
        await tester.tap(find.widgetWithText(OutlinedButton, 'Roll back'));
        await wait(tester);
        await shot('status_rollback');
        await close();
      },
    );
    await step(
      'Production deployment with a pending approval banner.',
      () async {
        await tester.pageBack();
        await wait(tester, 1500);
        await tester.tap(find.textContaining('waiting').first);
        await waitFor(tester, find.text('Timeline'));
        await wait(tester, 2500);
        await shot('status_pending_banner');
        await tester.pageBack();
        await wait(tester, 1000);
      },
    );
  }

  testWidgets('deployment UI gallery', (tester) async {
    tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
      const MethodChannel('plugins.it_nomads.com/flutter_secure_storage'),
      (call) async => null,
    );
    final originalOnError = FlutterError.onError;
    FlutterError.onError = (details) {
      // ignore: avoid_print
      print('FLUTTER_ERROR ${details.exceptionAsString().split('\n').first}');
      final text = details.exceptionAsString();
      if (text.contains('multiple heroes that share the same tag')) return;
      final where = details.context?.toString() ?? '';
      problems.add('${text.split('\n').first} | $where');
    };
    addTearDown(() {
      FlutterError.onError = originalOnError;
    });

    try {
      await run(tester, 'wide', const Size(1280, 800));
      await run(tester, 'narrow', const Size(900, 640));
    } finally {
      FlutterError.onError = originalOnError;
    }

    // ignore: avoid_print
    print('LAYOUT_PROBLEMS ${problems.length}');
    for (final p in problems.toSet()) {
      // ignore: avoid_print
      print('LAYOUT_PROBLEM $p');
    }
  });
}
