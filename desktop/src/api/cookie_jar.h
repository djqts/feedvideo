#pragma once

#include <QNetworkCookieJar>
#include <QString>

// Cookies must outlive the process: the gateway keeps the refresh token in an
// HttpOnly cookie, so losing the jar on exit means logging in again every run.
//
// Primary location is the per-user application data directory. When that is not
// writable (locked-down or sandboxed session), the jar falls back to a sealed
// blob next to the executable, protected with the platform secret box so the
// refresh token is not lying around in clear text.
class CookieJar : public QNetworkCookieJar
{
    Q_OBJECT

public:
    explicit CookieJar(QObject *parent = nullptr);
    ~CookieJar() override;

    bool insertCookie(const QNetworkCookie &cookie) override;
    bool deleteCookie(const QNetworkCookie &cookie) override;
    bool setCookiesFromUrl(const QList<QNetworkCookie> &cookieList, const QUrl &url) override;
    void clear();

    Q_INVOKABLE QString storageDescription() const;

private:
    void load();
    void save() const;
    bool writeFile(const QString &path, const QByteArray &bytes) const;
    void applyJson(const QByteArray &json);

    QString m_primaryPath;
    QString m_fallbackPath;
    mutable bool m_usingFallback = false;
    mutable bool m_fallbackSealed = true;
};
