#include <QGuiApplication>
#include <QFontDatabase>
#include <QQmlApplicationEngine>
#include <QQmlContext>
#include <QQuickStyle>
#include <QQuickWindow>
#include <QStandardPaths>
#include <QUrl>
#include <QDebug>

#include "api/api_client.h"
#include "native/frameless_window.h"
#include "theme/theme.h"

int main(int argc, char *argv[])
{
    QQuickStyle::setStyle(QStringLiteral("Basic"));

    QGuiApplication app(argc, argv);
    app.setOrganizationName(QStringLiteral("FeedVideo"));
    app.setApplicationName(QStringLiteral("FeedVideo Desktop"));
    app.setApplicationVersion(QStringLiteral("0.1.0"));

    // Saira is sliced into two fixed widths by tools/build_fonts.py.
    const QStringList fonts = {
        QStringLiteral(":/assets/fonts/Saira112-Regular.ttf"),
        QStringLiteral(":/assets/fonts/Saira112-SemiBold.ttf"),
        QStringLiteral(":/assets/fonts/Saira125-SemiBold.ttf"),
        QStringLiteral(":/assets/fonts/Saira125-Bold.ttf"),
        QStringLiteral(":/assets/fonts/Saira125-ExtraBold.ttf"),
        // Noto Sans CJK SC, sliced to GB2312 by tools/build_cjk_fonts.py.
        QStringLiteral(":/assets/fonts/FeedVideoSans-Regular.otf"),
        QStringLiteral(":/assets/fonts/FeedVideoSans-Bold.otf"),
    };
    for (const auto &path : fonts) {
        if (QFontDatabase::addApplicationFont(path) < 0)
            qWarning() << "failed to load font" << path;
    }

    // Latin comes from Saira; Chinese must come from the bundled sans rather
    // than whatever the system picked. Keep the system families behind ours so
    // characters outside the GB2312 slice still render.
    QStringList hanFallback = QFontDatabase::applicationFallbackFontFamilies(QChar::Script_Han);
    hanFallback.removeAll(QStringLiteral("FeedVideo Sans"));
    hanFallback.prepend(QStringLiteral("FeedVideo Sans"));
    QFontDatabase::setApplicationFallbackFontFamilies(QChar::Script_Han, hanFallback);

    Theme theme;
    ApiClient api;

    qmlRegisterSingletonInstance("FeedVideo.Core", 1, 0, "Theme", &theme);
    qmlRegisterSingletonInstance("FeedVideo.Core", 1, 0, "Api", &api);

    QQmlApplicationEngine engine;
    // Dev hook: FEEDVIDEO_DEMO=publish opens a screen at startup, which is the
    // only way to inspect a state when synthetic input is unavailable.
    engine.rootContext()->setContextProperty(QStringLiteral("demoMode"),
                                             qEnvironmentVariable("FEEDVIDEO_DEMO"));
    QObject::connect(
        &engine, &QQmlApplicationEngine::objectCreationFailed, &app,
        []() { QCoreApplication::exit(1); }, Qt::QueuedConnection);

    const QString devQml = qEnvironmentVariable("FEEDVIDEO_QML");
    const QUrl url = devQml.isEmpty() ? QUrl(QStringLiteral("qrc:/qml/Main.qml"))
                                      : QUrl::fromLocalFile(devQml + QStringLiteral("/Main.qml"));
    engine.load(url);
    if (engine.rootObjects().isEmpty())
        return 1;

    auto *window = qobject_cast<QQuickWindow *>(engine.rootObjects().constFirst());
    if (!window)
        return 1;

    window->setMinimumSize(QSize(1024, 680));
    auto *frameless = new FramelessWindow(window);
    QObject::connect(&app, &QCoreApplication::aboutToQuit, frameless, &QObject::deleteLater);

    return app.exec();
}
