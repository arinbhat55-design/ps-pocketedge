import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;

import 'package:app/api/api_client.dart';
import 'package:app/features/shell/app_shell.dart';

void main() {
  Future<void> pumpShell(WidgetTester tester, Size size) async {
    tester.view.physicalSize = size;
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    final client = ApiClient(
      baseUrl: 'http://localhost:8080',
      httpClient: _FakeClient(),
    );
    await tester.pumpWidget(
      MaterialApp(
        home: AppShell(apiClient: client, onLogout: () {}),
      ),
    );
    await tester.pumpAndSettle();
  }

  testWidgets('phones get a bottom bar with a More sheet', (tester) async {
    await pumpShell(tester, const Size(390, 844));

    expect(find.byType(NavigationRail), findsNothing);
    expect(find.byType(NavigationBar), findsOneWidget);

    await tester.tap(find.text('More'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Volumes'));
    await tester.pumpAndSettle();

    // The More slot now shows which overflow module is open.
    expect(
      find.descendant(
        of: find.byType(NavigationBar),
        matching: find.text('Volumes'),
      ),
      findsOneWidget,
    );
  });

  testWidgets('desktop keeps the side rail', (tester) async {
    await pumpShell(tester, const Size(1280, 900));

    expect(find.byType(NavigationRail), findsOneWidget);
    expect(find.byType(NavigationBar), findsNothing);
    expect(
      find.descendant(
        of: find.byType(NavigationRail),
        matching: find.text('PS-pocketEdge'),
      ),
      findsOneWidget,
    );
    expect(
      tester
          .widget<NavigationRail>(find.byType(NavigationRail))
          .minExtendedWidth,
      336,
    );

    await tester.tap(find.byTooltip('Collapse menu'));
    await tester.pumpAndSettle();
    expect(
      tester.widget<NavigationRail>(find.byType(NavigationRail)).extended,
      isFalse,
    );
    expect(find.text('PS-pocketEdge'), findsNothing);

    await tester.tap(find.byTooltip('Expand menu'));
    await tester.pumpAndSettle();
    expect(
      tester.widget<NavigationRail>(find.byType(NavigationRail)).extended,
      isTrue,
    );
    expect(find.text('PS-pocketEdge'), findsOneWidget);

    await tester.drag(
      find.byKey(const ValueKey('sidebar-resize-handle')),
      const Offset(80, 0),
    );
    await tester.pumpAndSettle();
    expect(
      tester
          .widget<NavigationRail>(find.byType(NavigationRail))
          .minExtendedWidth,
      greaterThan(336),
    );

    await tester.drag(
      find.byKey(const ValueKey('sidebar-resize-handle')),
      const Offset(-300, 0),
    );
    await tester.pumpAndSettle();
    expect(
      tester
          .widget<NavigationRail>(find.byType(NavigationRail))
          .minExtendedWidth,
      256,
    );
    expect(tester.takeException(), isNull);
  });

  testWidgets('short desktop windows can scroll the side rail', (tester) async {
    await pumpShell(tester, const Size(1280, 500));

    expect(find.byType(NavigationRail), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('narrow desktop rail can expand on demand', (tester) async {
    await pumpShell(tester, const Size(760, 700));

    expect(
      tester.widget<NavigationRail>(find.byType(NavigationRail)).extended,
      isFalse,
    );
    await tester.tap(find.byTooltip('Expand menu'));
    await tester.pumpAndSettle();
    expect(
      tester.widget<NavigationRail>(find.byType(NavigationRail)).extended,
      isTrue,
    );
    expect(find.text('PS-pocketEdge'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });
}

class _FakeClient extends http.BaseClient {
  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    final body = request.url.path == '/api/settings/access'
        ? jsonEncode({'requireLocalLogin': false, 'localListener': true})
        : request.url.path == '/api/auth/me'
        ? jsonEncode({
            'id': 'u1',
            'email': 'me@example.com',
            'role': 'viewer',
            'createdAt': '2026-01-01T00:00:00Z',
          })
        : '[]';
    return http.StreamedResponse(Stream.value(utf8.encode(body)), 200);
  }
}
