#pragma once

#include <QJsonDocument>
#include <QObject>
#include <QHash>
#include <QSet>
#include <QUrl>
#include <QVariant>
#include <QVariantMap>

class QNetworkAccessManager;
class QNetworkReply;
class CookieJar;

// Thin async client over the FeedVideo gateway.
//
// Call shape is tag-based so QML stays declarative:
//   Api.request("feed.page1", "GET", "/v1/feed", { type: "hot", page: 1, size: 20 })
//   Connections { target: Api; function onResult(tag, data) { if (tag === "feed.page1") ... } }
class ApiClient : public QObject
{
    Q_OBJECT

    Q_PROPERTY(QString baseUrl READ baseUrl WRITE setBaseUrl NOTIFY baseUrlChanged)
    Q_PROPERTY(QString mediaBase READ mediaBase WRITE setMediaBase NOTIFY mediaBaseChanged)
    Q_PROPERTY(int state READ state NOTIFY stateChanged)
    Q_PROPERTY(QString lastError READ lastError NOTIFY lastErrorChanged)
    Q_PROPERTY(QVariantMap me READ me NOTIFY sessionChanged)
    Q_PROPERTY(bool authenticated READ authenticated NOTIFY sessionChanged)
    Q_PROPERTY(QString userName READ userName NOTIFY sessionChanged)
    Q_PROPERTY(QString sessionStore READ sessionStore NOTIFY stateChanged)

public:
    enum State {
        Idle = 0,
        Probing = 1,
        Ready = 2,
        Offline = 3,
        Unauthorized = 4,
    };
    Q_ENUM(State)

    explicit ApiClient(QObject *parent = nullptr);

    QString baseUrl() const { return m_baseUrl; }
    void setBaseUrl(const QString &url);
    // Where /storage/... resolves to. The gateway does not proxy object storage,
    // so in development this is the MinIO endpoint.
    QString mediaBase() const { return m_mediaBase; }
    void setMediaBase(const QString &url);
    int state() const { return m_state; }
    QString lastError() const { return m_lastError; }
    QVariantMap me() const { return m_me; }
    bool authenticated() const { return !m_me.isEmpty(); }
    QString userName() const;
    // Where the session cookies are kept (user dir, or the sealed workspace copy).
    QString sessionStore() const;

    Q_INVOKABLE void probe();
    Q_INVOKABLE void request(const QString &tag, const QString &method, const QString &path,
                             const QVariantMap &body = QVariantMap());
    // Same as request() but with extra headers (publishing needs Idempotency-Key).
    Q_INVOKABLE void requestWithHeaders(const QString &tag, const QString &method, const QString &path,
                                        const QVariantMap &body, const QVariantMap &headers);
    Q_INVOKABLE void requestQuery(const QString &tag, const QString &path, const QVariantMap &query);
    Q_INVOKABLE void login(const QString &username, const QString &password);
    Q_INVOKABLE void registerAccount(const QString &username, const QString &password);
    Q_INVOKABLE void fetchMe();
    Q_INVOKABLE void logout();
    Q_INVOKABLE void applyMe(const QVariantMap &me);

    // Multipart upload with progress. The gateway contract is:
    //   POST /v1/upload with parts "video" (required) and "cover" (optional)
    //   -> { upload_id, video_url, cover_url, ... }
    Q_INVOKABLE void upload(const QString &tag, const QString &videoPath, const QString &coverPath = QString());

signals:
    void result(const QString &tag, const QVariant &data);
    void failed(const QString &tag, int httpStatus, const QString &code, const QString &message);
    void uploadProgress(const QString &tag, qreal progress);
    void stateChanged();
    void sessionChanged();
    void baseUrlChanged();
    void mediaBaseChanged();
    void lastErrorChanged();

private:
    struct Pending
    {
        QString tag;
        QString method;
        QUrl url;
        QByteArray body;
        QString contentType;
        QVariantMap headers;
    };

    QUrl endpoint(const QString &path, const QVariantMap &query) const;
    QUrl originUrl(const QString &path) const;
    QNetworkReply *send(const QString &tag, const QString &method, const QUrl &url, const QByteArray &body,
                        const QString &contentType, const QVariantMap &headers = QVariantMap());
    void handleReply(QNetworkReply *reply, const QString &tag);
    void refreshSession();
    void replayAfterRefresh();
    void setState(int state);
    void setLastError(const QString &error);
    QVariant unwrap(const QJsonDocument &doc);
    QString csrfToken() const;

    QNetworkAccessManager *m_nam = nullptr;
    CookieJar *m_jar = nullptr;
    QString m_baseUrl = QStringLiteral("http://127.0.0.1:18080/api");
    QString m_mediaBase = QStringLiteral("http://127.0.0.1:9000");
    int m_state = Idle;
    QString m_lastError;
    QVariantMap m_me;

    // 401 recovery: the access token lives 15 minutes, the refresh cookie a
    // week, so the first 401 of a burst triggers one refresh and a replay.
    bool m_refreshing = false;
    QHash<QString, Pending> m_pending;   // last request per tag
    QSet<QString> m_replayed;            // tags already replayed after a refresh
};
