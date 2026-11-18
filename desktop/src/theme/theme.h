#pragma once

#include <QColor>
#include <QEasingCurve>
#include <QHash>
#include <QObject>
#include <QString>

// Theme is the single place where the endfield-ui tokens for this product live.
// QML never hard-codes a colour, a cut size or a duration; it reads Theme.*.
//
// Rooms: the product has two complete themes (paper / stage) plus one constant
// surface (the player canvas). A screen declares its room once, here.
class Theme : public QObject
{
    Q_OBJECT

    Q_PROPERTY(int mode READ mode NOTIFY changed)            // 0 = paper, 1 = stage
    Q_PROPERTY(int preference READ preference WRITE setPreference NOTIFY changed) // 0 auto, 1 paper, 2 stage
    Q_PROPERTY(QString room READ room NOTIFY changed)
    Q_PROPERTY(qreal density READ density NOTIFY changed)
    Q_PROPERTY(bool reduceMotion READ reduceMotion WRITE setReduceMotion NOTIFY changed)

    Q_PROPERTY(QColor paper READ paper NOTIFY changed)
    Q_PROPERTY(QColor panel READ panel NOTIFY changed)
    Q_PROPERTY(QColor ink READ ink NOTIFY changed)
    Q_PROPERTY(QColor ink2 READ ink2 NOTIFY changed)
    Q_PROPERTY(QColor rule READ rule NOTIFY changed)
    Q_PROPERTY(QColor chip READ chip NOTIFY changed)
    Q_PROPERTY(QColor signal READ signal NOTIFY changed)
    Q_PROPERTY(QColor onSignal READ onSignal NOTIFY changed)
    Q_PROPERTY(QColor trace READ trace NOTIFY changed)
    Q_PROPERTY(QColor tagBg READ tagBg NOTIFY changed)
    Q_PROPERTY(QColor tagFg READ tagFg NOTIFY changed)
    Q_PROPERTY(QColor danger READ danger NOTIFY changed)
    Q_PROPERTY(QColor scrim READ scrim NOTIFY changed)
    Q_PROPERTY(QColor commandBar READ commandBar NOTIFY changed)
    Q_PROPERTY(QColor commandBarText READ commandBarText NOTIFY changed)
    Q_PROPERTY(QColor commandBarInk READ commandBarInk NOTIFY changed)
    Q_PROPERTY(QColor triA READ triA NOTIFY changed)
    Q_PROPERTY(QColor triB READ triB NOTIFY changed)
    Q_PROPERTY(QColor triC READ triC NOTIFY changed)

    // corner cuts, already multiplied by the tight factor (0.6)
    Q_PROPERTY(qreal cutSmall READ cutSmall NOTIFY changed)
    Q_PROPERTY(qreal cutButton READ cutButton NOTIFY changed)
    Q_PROPERTY(qreal cutPanel READ cutPanel NOTIFY changed)
    Q_PROPERTY(qreal cutFrame READ cutFrame NOTIFY changed)

    Q_PROPERTY(qreal pagePad READ pagePad NOTIFY changed)
    Q_PROPERTY(qreal gap READ gap NOTIFY changed)
    Q_PROPERTY(qreal rowPad READ rowPad NOTIFY changed)

    Q_PROPERTY(QEasingCurve snap READ snap CONSTANT)
    Q_PROPERTY(QEasingCurve mech READ mech CONSTANT)
    Q_PROPERTY(QEasingCurve ease READ easeCurve CONSTANT)

    Q_PROPERTY(int dPress READ dPress CONSTANT)
    Q_PROPERTY(int dRow READ dRow CONSTANT)
    Q_PROPERTY(int dWipe READ dWipe CONSTANT)
    Q_PROPERTY(int dCome READ dCome CONSTANT)
    Q_PROPERTY(int dSheet READ dSheet CONSTANT)
    Q_PROPERTY(int dDialog READ dDialog CONSTANT)
    Q_PROPERTY(int dToast READ dToast CONSTANT)

    Q_PROPERTY(QString fontWide READ fontWide CONSTANT)   // width 125: labels, wordmark
    Q_PROPERTY(QString fontField READ fontField CONSTANT) // width 112: fields, figures
    Q_PROPERTY(QString fontText READ fontText CONSTANT)   // CJK body
    Q_PROPERTY(QString qtVersion READ qtVersion CONSTANT)

public:
    explicit Theme(QObject *parent = nullptr);

    int mode() const { return m_mode; }
    int preference() const { return m_preference; }
    void setPreference(int p);
    QString room() const { return m_room; }
    qreal density() const { return m_density; }
    bool reduceMotion() const { return m_reduce; }
    void setReduceMotion(bool r);

    QColor paper() const;
    QColor panel() const;
    QColor ink() const;
    QColor ink2() const;
    QColor rule() const;
    QColor chip() const;
    QColor signal() const;
    QColor onSignal() const;
    QColor trace() const;
    QColor tagBg() const;
    QColor tagFg() const;
    QColor danger() const;
    QColor scrim() const;
    QColor commandBar() const { return QColor("#161616"); }
    QColor commandBarText() const { return QColor("#EDEDED"); }
    QColor commandBarInk() const { return QColor("#5A5A5A"); }
    QColor triA() const { return QColor("#FF00F0"); }
    QColor triB() const { return QColor("#FFFA00"); }
    QColor triC() const { return QColor("#00FFA2"); }

    qreal cutSmall() const;
    qreal cutButton() const;
    qreal cutPanel() const;
    qreal cutFrame() const;

    qreal pagePad() const { return 40 * m_density; }
    qreal gap() const { return 20 * m_density; }
    qreal rowPad() const { return 12 * m_density; }

    QEasingCurve snap() const;
    QEasingCurve mech() const;
    QEasingCurve easeCurve() const;

    int dPress() const { return 100; }
    int dRow() const { return 200; }
    int dWipe() const { return 280; }
    int dCome() const { return 320; }
    int dSheet() const { return 360; }
    int dDialog() const { return 280; }
    int dToast() const { return 280; }

    QString fontWide() const { return QStringLiteral("Saira125"); }
    QString fontField() const { return QStringLiteral("Saira112"); }
    QString fontText() const { return QStringLiteral("FeedVideo Sans"); }
    QString qtVersion() const;

    // Called by the router: one place decides the room and the density of a screen.
    Q_INVOKABLE void enterScreen(const QString &screenId);
    Q_INVOKABLE void toggleMode();
    // Signal the app uses for "the theme changed because the room changed".
    Q_INVOKABLE qreal px(qreal value) const { return value * m_density; }

signals:
    void changed();

private:
    void refresh();
    qreal cutFactor() const;

    int m_mode = 1;        // stage by default: the feed is the landing screen
    int m_preference = 0;  // auto
    QString m_room = QStringLiteral("stage");
    qreal m_density = 1.0;
    bool m_reduce = false;
};
