import 'package:app/api/api_client.dart';
import 'package:app/features/servers/add_server_dialog.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

class _EnrollmentClient extends ApiClient {
  _EnrollmentClient() : super(baseUrl: 'http://localhost');

  @override
  Future<EnrollmentToken> createEnrollmentToken() async => EnrollmentToken(
    token: 'test-enrollment',
    expiresAt: DateTime(2026, 10, 2),
    installHint: '',
  );
}

void main() {
  testWidgets('Runtime and platform select the appropriate installer', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(1000, 1000);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(body: AddServerDialog(apiClient: _EnrollmentClient())),
      ),
    );
    await tester.pumpAndSettle();

    String command() =>
        tester.widget<SelectableText>(find.byType(SelectableText)).data!;
    expect(command(), contains('sudo sh'));
    expect(command(), contains('--runtime=docker'));
    await tester.tap(find.text('Podman'));
    await tester.pumpAndSettle();
    expect(command(), contains('--runtime=podman'));
    expect(find.textContaining('rootful Podman socket'), findsOneWidget);
    await tester.tap(find.text('macOS'));
    await tester.pumpAndSettle();
    expect(command(), contains('install-agent-macos.sh'));
    expect(command(), isNot(contains('sudo')));
    expect(find.textContaining('podman machine start'), findsOneWidget);
    await tester.tap(find.text('Windows'));
    await tester.pumpAndSettle();
    expect(command(), contains('install-agent.sh'));
    expect(command(), contains('--runtime=podman'));
    expect(find.textContaining('WSL2'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });
  testWidgets('Podman setup fits a small phone screen', (tester) async {
    tester.view.physicalSize = const Size(390, 640);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(body: AddServerDialog(apiClient: _EnrollmentClient())),
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.text('Podman'));
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
    await tester.ensureVisible(find.byType(SelectableText));
    expect(find.text('Close').hitTestable(), findsOneWidget);
  });
}
