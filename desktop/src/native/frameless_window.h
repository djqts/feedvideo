#pragma once

#include <QAbstractNativeEventFilter>
#include <QObject>
#include <QWindow>

// Turns a Qt.FramelessWindowHint window into a real Win32 window that still
// behaves like one: edge resize, drag-to-move, double-click maximize, Win11
// snap, and a square (not rounded) outer edge.
//
// The QML title bar never implements dragging itself: it calls
// Window.window.startSystemMove(), which keeps snap layouts and the native
// "shake to minimise" behaviour intact.
class FramelessWindow : public QObject, public QAbstractNativeEventFilter
{
    Q_OBJECT

public:
    explicit FramelessWindow(QWindow *window);
    ~FramelessWindow() override;

    bool nativeEventFilter(const QByteArray &eventType, void *message, qintptr *result) override;

    Q_INVOKABLE void toggleMaximize();
    Q_INVOKABLE bool maximized() const;

private:
    QWindow *m_window = nullptr;
    bool m_installed = false;
};
