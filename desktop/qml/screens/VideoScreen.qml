import QtQuick
import QtQuick.Controls
import FeedVideo.Core 1.0
import "../components"
import "../lib/Http.js" as Http

// The video itself. The stage is ink in both themes because the picture is the
// only thing that should be bright; the information around it is paper.
Item {
    id: root

    property var router: null
    property string videoId: ""
    property var detail: ({})
    property var comments: []
    property int commentTotal: 0
    property int commentPage: 1
    property bool loadingMore: false
    property string commentCursor: ""
    property bool commentHasMore: false
    property int commentRequestToken: 0
    property bool commentResetRequest: true
    // Delegates are recreated whenever the model changes; remembering which
    // comments the reader expanded has to live on the screen, not the row.
    property var expandedIds: ({})
    property var pendingComment: null
    property string pendingPostParent: ""
    property bool loading: false
    property string errorText: ""
    property bool liked: false
    property bool collected: false
    property int likeCount: 0
    property int collectCount: 0
    property string commentDraft: ""
    property bool posting: false
    property var reportTarget: null
    property var replyTarget: null
    property string infoText: ""
    property bool demoReported: false
    property bool gone: false
    property bool gonePending: false

    readonly property var info: detail && detail.video ? detail.video : detail
    readonly property string title: Http.firstOf(info.title, qsTr("未命名视频"))
    readonly property string author: Http.author(info)
    readonly property string cover: Http.mediaUrl(info.cover_url || info.cover || "", Api.mediaBase, Api.baseUrl)
    readonly property string playable: Http.mediaUrl(info.transcoded_url || info.video_url || info.play_url || "", Api.mediaBase, Api.baseUrl)
    readonly property string description: Http.firstOf(info.description, "")

    // Small underlined text that acts as a link; used by the comment rows so the
    // hit area is the label itself, not a Row child with anchors.
    component ActionLink: Item {
        id: link

        property string label: ""
        property color tone: Theme.ink2
        property bool underline: false
        property bool bold: false

        signal activated()

        width: linkLabel.width
        height: linkLabel.height

        Text {
            id: linkLabel
            text: link.label
            color: link.tone
            font.family: Theme.fontText
            font.pixelSize: 11
            font.weight: link.bold ? Font.DemiBold : Font.Normal
            font.underline: link.underline
            renderType: Text.NativeRendering
        }

        MouseArea {
            anchors.fill: parent
            anchors.margins: -6
            cursorShape: Qt.PointingHandCursor
            onClicked: link.activated()
        }
    }

    // One level of replies, embedded in the parent comment by the server.
    component ReplyRow: Item {
        id: replyRow

        property var reply: null
        property bool expanded: false

        width: parent ? parent.width : 0
        height: replyBody.implicitHeight + 10

        Rectangle {
            width: 1
            height: parent.height
            color: Theme.rule
        }

        Column {
            id: replyBody
            anchors.left: parent.left
            anchors.leftMargin: 12
            anchors.right: parent.right
            spacing: 3

            Row {
                spacing: 8

                Text {
                    text: Http.author(replyRow.reply)
                    color: Theme.ink
                    font.family: Theme.fontText
                    font.pixelSize: 12
                    font.weight: Font.DemiBold
                    renderType: Text.NativeRendering
                }

                Text {
                    text: Http.date(replyRow.reply.created_at)
                    color: Theme.ink2
                    font.family: Theme.fontField
                    font.pixelSize: 10
                    renderType: Text.NativeRendering
                }
            }

            Text {
                id: replyContent
                width: parent.width
                text: Http.firstOf(replyRow.reply.content, "")
                color: Theme.ink
                font.family: Theme.fontText
                font.pixelSize: 12
                wrapMode: Text.WordWrap
                elide: Text.ElideRight
                maximumLineCount: replyRow.expanded ? 999 : 2
                renderType: Text.NativeRendering
            }

            Row {
                spacing: 14

                ActionLink {
                    label: replyRow.reply && replyRow.reply.is_liked
                        ? qsTr("已赞 ") + Http.count(replyRow.reply.like_count)
                        : qsTr("赞 ") + Http.count(replyRow.reply.like_count)
                    tone: replyRow.reply && replyRow.reply.is_liked ? Theme.ink : Theme.ink2
                    bold: replyRow.reply && !!replyRow.reply.is_liked
                    onActivated: root.toggleCommentLike(replyRow.reply)
                }

                ActionLink {
                    visible: root.isMine(replyRow.reply)
                    label: qsTr("删除")
                    tone: Theme.danger
                    onActivated: root.deleteComment(replyRow.reply)
                }

                ActionLink {
                    visible: Api.authenticated && !root.isMine(replyRow.reply)
                    label: qsTr("举报")
                    onActivated: root.askReport(replyRow.reply)
                }

                ActionLink {
                    visible: replyContent.truncated || replyRow.expanded
                    label: replyRow.expanded ? qsTr("收起") : qsTr("展开")
                    tone: Theme.ink
                    underline: true
                    onActivated: replyRow.expanded = !replyRow.expanded
                }
            }
        }
    }

    function open(id) {
        if (!id)
            return
        videoId = id
        loading = true
        errorText = ""
        gone = false
        commentPage = 1
        commentTotal = 0
        comments = []
        Api.request("video.detail", "GET", "/v1/videos/" + id)
        loadComments(true)
    }

    // Cursor paging: the first page has no cursor, every later page asks for what
    // follows the last id we have, so a comment inserted meanwhile cannot shift
    // the window and make rows repeat or vanish.
    function loadComments(reset) {
        if (videoId === "")
            return
        if (reset) {
            comments = []
            expandedIds = ({})
            commentCursor = ""
            commentHasMore = false
            commentPage = 1
        }
        loadingMore = !reset
        commentResetRequest = reset
        commentRequestToken = commentRequestToken + 1
        var query = { page: 1, page_size: 20 }
        if (!reset && commentCursor !== "")
            query.before = commentCursor
        Api.requestQuery("video.comments:" + commentRequestToken, "/v1/videos/" + videoId + "/comments", query)
    }

    function reloadComments() {
        loadComments(true)
    }

    // Ignore anything that is already in the list, whatever page it came from.
    function toggleCommentExpanded(comment) {
        if (!comment)
            return
        var key = String(comment.id)
        var next = {}
        for (var k in root.expandedIds)
            next[k] = root.expandedIds[k]
        if (next[key])
            delete next[key]
        else
            next[key] = true
        root.expandedIds = next
    }

    function appendUnique(existing, batch) {
        var seen = {}
        for (var i = 0; i < existing.length; i++)
            seen[String(existing[i].id)] = true
        var out = existing.slice()
        for (var j = 0; j < batch.length; j++) {
            if (!seen[String(batch[j].id)])
                out.push(batch[j])
        }
        return out
    }

    function queueOrInsertComment(comment) {
        // Inserting at the top while the reader is scrolled down would jump the
        // list under them, so park it behind a pill instead.
        if (commentList.contentY > 2) {
            pendingComment = comment
            return
        }
        comments = [comment].concat(comments)
        commentTotal = commentTotal + 1
    }

    function showPendingComment() {
        if (!pendingComment)
            return
        comments = [pendingComment].concat(comments)
        commentTotal = commentTotal + 1
        pendingComment = null
        commentList.positionViewAtBeginning()
    }

    function postComment() {
        var text = commentDraft.trim()
        if (text === "" || videoId === "")
            return
        posting = true
        errorText = ""
        pendingPostParent = root.replyTarget && root.replyTarget.id ? String(root.replyTarget.id) : ""
        var body = { content: text }
        // Replies are one level deep; the server rejects a reply to a reply.
        if (pendingPostParent !== "")
            body.parent_id = pendingPostParent
        Api.request("comment.post", "POST", "/v1/videos/" + videoId + "/comments", body)
    }

    function isMine(comment) {
        var uid = Http.firstOf(Api.me.uid, Api.me.id, "")
        return uid !== "" && String(comment.user_id) === uid
    }

    // Like is a state, not a toggle: POST sets it, DELETE clears it.
    function toggleCommentLike(comment) {
        if (!comment || !Api.authenticated)
            return
        var liked = !!comment.is_liked
        Api.request("comment.like:" + comment.id, liked ? "DELETE" : "POST",
                    "/v1/comments/" + comment.id + "/like", {})
    }

    function deleteComment(comment) {
        if (!comment)
            return
        Api.request("comment.delete:" + comment.id, "DELETE", "/v1/comments/" + comment.id)
    }

    function askReport(comment) {
        root.reportTarget = comment
        dialog.ask({
            eyebrow: "REPORT",
            question: qsTr("举报这条评论？"),
            detail: qsTr("选一个原因提交给平台。举报不会公开，作者也看不到。"),
            confirmLabel: qsTr("提交举报"),
            options: [
                { label: qsTr("垃圾广告"), value: "spam" },
                { label: qsTr("人身攻击"), value: "abuse" },
                { label: qsTr("违法内容"), value: "illegal" },
                { label: qsTr("其他"), value: "other" }
            ]
        })
    }

    function submitReport(reason) {
        var comment = root.reportTarget
        if (!comment || reason === "")
            return
        Api.request("comment.report:" + comment.id, "POST",
                    "/v1/comments/" + comment.id + "/report", { reason: reason })
    }

    ConfirmDialog {
        id: dialog
        onConfirmed: root.submitReport(selectedValue)
    }

    function applyDetail(data) {        var payload = data && data.data ? data.data : data
        detail = payload || ({})
        likeCount = Http.likes(info)
        collectCount = Http.collects(info)
        liked = !!(info.liked || info.is_liked)
        collected = !!(info.collected || info.is_collected)
    }

    Connections {
        target: Api

        function onResult(tag, data) {
            if (tag === "video.detail") {
                root.loading = false
                root.gone = false
                root.infoText = ""
                root.applyDetail(data)
            } else if (tag.indexOf("video.comments:") === 0) {
                var token = Http.num(tag.substring("video.comments:".length))
                if (token !== root.commentRequestToken)
                    return // a newer request already replaced this one
                var pagePayload = data && data.data ? data.data : data
                var batch = Http.items({ data: pagePayload })
                root.loadingMore = false
                root.commentHasMore = !!(pagePayload && pagePayload.has_more)
                root.commentCursor = pagePayload && pagePayload.next_before ? String(pagePayload.next_before) : ""
                root.comments = root.commentResetRequest ? batch : root.appendUnique(root.comments, batch)
                if (pagePayload && pagePayload.total !== undefined)
                    root.commentTotal = Http.num(pagePayload.total)
                if (demoMode === "report" && !root.demoReported && root.comments.length > 0) {
                    root.demoReported = true
                    root.askReport(root.comments[0])
                }
            } else if (tag === "comment.post") {
                root.posting = false
                var posted = data && data.data ? data.data : data
                var created = { 
                    id: posted && posted.comment_id ? String(posted.comment_id) : "",
                    content: root.commentDraft,
                    user_id: Http.firstOf(Api.me.uid, Api.me.id, ""),
                    created_at: new Date().toISOString(),
                    like_count: 0,
                    is_liked: false,
                    replies: []
                }
                if (root.pendingPostParent !== "") {
                    var parentId = root.pendingPostParent
                    var list = root.comments.slice()
                    for (var i = 0; i < list.length; i++) {
                        if (String(list[i].id) !== parentId)
                            continue
                        var merged = {}
                        for (var key in list[i])
                            merged[key] = list[i][key]
                        merged.replies = Http.toArray(list[i].replies).concat([created])
                        list[i] = merged
                    }
                    root.comments = list
                } else {
                    root.queueOrInsertComment(created)
                }
                root.pendingPostParent = ""
                root.commentDraft = ""
                root.replyTarget = null
            } else if (tag.indexOf("comment.like:") === 0) {
                // The like endpoint returns no counters, so the client flips the
                // state it just set (POST = like, DELETE = unlike).
                var likeId = tag.substring("comment.like:".length)
                var updated = root.comments.slice()
                for (var i = 0; i < updated.length; i++) {
                    if (String(updated[i].id) !== likeId)
                        continue
                    var wasLiked = !!updated[i].is_liked
                    var next = {}
                    for (var key in updated[i])
                        next[key] = updated[i][key]
                    next.is_liked = !wasLiked
                    next.like_count = Math.max(0, Http.num(updated[i].like_count) + (wasLiked ? -1 : 1))
                    updated[i] = next
                }
                root.comments = updated
            } else if (tag.indexOf("comment.delete:") === 0) {
                var goneId = tag.substring("comment.delete:".length)
                var kept = []
                for (var j = 0; j < root.comments.length; j++) {
                    if (String(root.comments[j].id) !== goneId)
                        kept.push(root.comments[j])
                }
                root.comments = kept
                root.commentTotal = Math.max(0, root.commentTotal - 1)
            } else if (tag.indexOf("comment.report:") === 0) {
                root.errorText = ""
                root.infoText = qsTr("已提交举报，平台会按社区规则处理。")
            }
        }

        function onFailed(tag, status, code, message) {
            if (tag === "video.detail") {
                root.loading = false
                if (status === 404) {
                    // Deleted, or still in review: either way there is nothing
                    // honest to show here, so the page says so and offers a way
                    // back instead of leaving stale content on screen.
                    root.gone = true
                    root.gonePending = message.indexOf("pending") >= 0
                    root.detail = ({})
                    root.comments = []
                    root.errorText = ""
                } else {
                    root.errorText = message
                }
            } else if (tag === "comment.post") {
                root.posting = false
                if (status === 401)
                    root.errorText = qsTr("登录之后才能发表评论。")
                else
                    root.errorText = message
            } else if (tag.indexOf("comment.like:") === 0) {
                root.errorText = status === 401 ? qsTr("登录之后才能点赞。") : message
            } else if (tag.indexOf("comment.delete:") === 0) {
                root.errorText = status === 403 ? qsTr("只能删除自己的评论。") : message
            } else if (tag.indexOf("comment.report:") === 0) {
                root.errorText = status === 429 ? qsTr("举报太频繁了，过一会儿再试。") : message
            }
        }
    }

    Connections {
        target: root.router
        function onNavigated(name) {
            if (name === "video")
                root.open(root.router.params && root.router.params.id)
        }
        // The author deleted it from the publish screen while this page was open.
        function onVideoDeleted(id) {
            if (root.videoId !== id)
                return
            root.gone = true
            root.gonePending = false
            root.detail = ({})
            root.comments = []
            root.commentTotal = 0
        }
    }

    Component.onCompleted: {
        if (root.router && root.router.params && root.router.params.id)
            open(root.router.params.id)
    }

    Row {
        anchors.fill: parent
        anchors.margins: Theme.pagePad
        spacing: 26
        visible: !root.gone

        // Main column
        Column {
            width: parent.width - side.width - 26
            spacing: 14

            PlayerBay {
                width: parent.width
                height: Math.min(parent.width * 0.5625, root.height * 0.46)
                source: root.playable
                poster: root.cover
            }

            Row {
                width: parent.width
                spacing: 12

                Column {
                    width: parent.width
                    spacing: 4

                    Text {
                        text: root.title
                        color: Theme.ink
                        font.family: Theme.fontText
                        font.pixelSize: 26
                        font.weight: Font.Bold
                        wrapMode: Text.WordWrap
                        width: parent.width
                        renderType: Text.NativeRendering
                    }

                    FieldRow {
                        fields: [
                            root.author,
                            Http.date(root.info.created_at),
                            Http.count(Http.plays(root.info)) + qsTr(" 次播放")
                        ]
                    }
                }
            }

            TriStrip { width: 160 }

            Text {
                visible: root.description !== ""
                text: root.description
                color: Theme.ink
                font.family: Theme.fontText
                font.pixelSize: 15
                lineHeight: 1.7
                wrapMode: Text.WordWrap
                width: Math.min(parent.width, 640)
                renderType: Text.NativeRendering
            }

            Row {
                spacing: 10

                ActionButton {
                    label: qsTr("赞 ") + Http.count(root.likeCount)
                    icon: "heart"
                    variant: root.liked ? "secondary" : "ghost"
                    active: root.liked
                    cornerColor: Theme.paper
                    onClicked: root.toggleLike()
                }

                ActionButton {
                    label: qsTr("收藏 ") + Http.count(root.collectCount)
                    icon: "bookmark"
                    variant: root.collected ? "secondary" : "ghost"
                    active: root.collected
                    cornerColor: Theme.paper
                    onClicked: root.toggleCollect()
                }

                ActionButton {
                    label: qsTr("分享")
                    icon: "share"
                    variant: "ghost"
                    cornerColor: Theme.paper
                    onClicked: Api.request("share", "POST", "/v1/share/" + root.videoId)
                }

                ActionButton {
                    label: qsTr("返回推荐")
                    variant: "primary"
                    cornerColor: Theme.paper
                    onClicked: if (root.router) root.router.go("feed")
                }
            }

            Notice {
                width: parent.width
                visible: root.errorText !== ""
                text: root.errorText
                actionLabel: qsTr("重试")
                onActionClicked: root.open(root.videoId)
            }

            Notice {
                width: parent.width
                visible: root.infoText !== ""
                text: root.infoText
            }
        }

        // Comments
        Column {
            id: side
            width: 360
            height: parent.height
            spacing: 10

            PairTitle { en: "COMMENTS"; cn: qsTr("评论") }

            FieldRow {
                fields: [Http.count(root.commentTotal) + qsTr(" 条")]
                size: 11
            }

            ActionButton {
                id: newCommentPill
                z: 2
                anchors.top: parent.top
                anchors.horizontalCenter: parent.horizontalCenter
                visible: root.pendingComment !== null
                label: qsTr("有 1 条新评论")
                variant: "primary"
                cornerColor: Theme.paper
                onClicked: root.showPendingComment()
            }

            ListView {
                id: commentList
                width: parent.width
                height: Math.max(120, parent.height - 90 - composer.height - 12)
                clip: true
                model: root.comments
                spacing: 0
                boundsBehavior: Flickable.StopAtBounds

                delegate: Item {
                    id: commentRow
                    required property var modelData
                    required property int index

                    readonly property bool liked: !!modelData.is_liked
                    readonly property bool expanded: root.expandedIds[String(modelData.id)] === true
                    readonly property var replies: Http.toArray(modelData.replies)

                    width: side.width
                    height: commentColumn.implicitHeight + 30

                    Column {
                        id: commentColumn
                        anchors.left: parent.left
                        anchors.right: parent.right
                        anchors.top: parent.top
                        anchors.topMargin: 12
                        spacing: 4

                        Row {
                            spacing: 8

                            Text {
                                text: Http.author(modelData)
                                color: Theme.ink
                                font.family: Theme.fontText
                                font.pixelSize: 13
                                font.weight: Font.DemiBold
                                renderType: Text.NativeRendering
                            }

                            Text {
                                text: Http.date(modelData.created_at)
                                color: Theme.ink2
                                font.family: Theme.fontField
                                font.pixelSize: 10
                                renderType: Text.NativeRendering
                            }
                        }

                        Text {
                            id: contentText
                            text: Http.firstOf(modelData.content, modelData.text, "")
                            color: Theme.ink
                            font.family: Theme.fontText
                            font.pixelSize: 13
                            wrapMode: Text.WordWrap
                            width: parent.width
                            elide: Text.ElideRight
                            maximumLineCount: commentRow.expanded ? 999 : 2
                            renderType: Text.NativeRendering
                        }

                        Row {
                            spacing: 16

                            // A Row only positions its children, so the hit area
                            // has to be an Item around the content, not a child
                            // anchored to the Row itself.
                            Item {
                                width: likeContent.width
                                height: likeContent.height

                                Row {
                                    id: likeContent
                                    spacing: 4

                                    Icon {
                                        name: "heart"
                                        size: 12
                                        anchors.verticalCenter: parent.verticalCenter
                                        color: commentRow.liked ? Theme.ink : Theme.ink2
                                    }

                                    Text {
                                        anchors.verticalCenter: parent.verticalCenter
                                        text: commentRow.liked
                                            ? qsTr("已赞 ") + Http.count(modelData.like_count)
                                            : qsTr("赞 ") + Http.count(modelData.like_count)
                                        color: commentRow.liked ? Theme.ink : Theme.ink2
                                        font.family: Theme.fontText
                                        font.pixelSize: 11
                                        font.weight: commentRow.liked ? Font.DemiBold : Font.Normal
                                        renderType: Text.NativeRendering
                                    }
                                }

                                MouseArea {
                                    anchors.fill: parent
                                    anchors.margins: -6
                                    cursorShape: Api.authenticated ? Qt.PointingHandCursor : Qt.ArrowCursor
                                    onClicked: root.toggleCommentLike(commentRow.modelData)
                                }
                            }

                            Item {
                                visible: root.isMine(commentRow.modelData)
                                width: deleteLabel.width
                                height: deleteLabel.height

                                Text {
                                    id: deleteLabel
                                    text: qsTr("删除")
                                    color: Theme.danger
                                    font.family: Theme.fontText
                                    font.pixelSize: 11
                                    renderType: Text.NativeRendering
                                }

                                MouseArea {
                                    anchors.fill: parent
                                    anchors.margins: -6
                                    cursorShape: Qt.PointingHandCursor
                                    onClicked: root.deleteComment(commentRow.modelData)
                                }
                            }

                            Item {
                                visible: Api.authenticated && !root.isMine(commentRow.modelData)
                                width: reportLabel.width
                                height: reportLabel.height

                                Text {
                                    id: reportLabel
                                    text: qsTr("举报")
                                    color: Theme.ink2
                                    font.family: Theme.fontText
                                    font.pixelSize: 11
                                    renderType: Text.NativeRendering
                                }

                                MouseArea {
                                    anchors.fill: parent
                                    anchors.margins: -6
                                    cursorShape: Qt.PointingHandCursor
                                    onClicked: root.askReport(commentRow.modelData)
                                }
                            }

                            ActionLink {
                                visible: Api.authenticated
                                label: qsTr("回复")
                                tone: Theme.ink
                                onActivated: root.replyTarget = commentRow.modelData
                            }

                            ActionLink {
                                visible: contentText.truncated || commentRow.expanded
                                label: commentRow.expanded ? qsTr("收起") : qsTr("展开")
                                tone: Theme.ink
                                underline: true
                                onActivated: root.toggleCommentExpanded(commentRow.modelData)
                            }
                        }

                        // Replies, one level deep, in the order the server sent them.
                        Column {
                            width: parent.width
                            spacing: 8
                            visible: commentRow.replies.length > 0

                            Repeater {
                                model: commentRow.replies

                                delegate: ReplyRow {
                                    required property var modelData
                                    reply: modelData
                                    width: parent.width
                                }
                            }
                        }
                    }

                    Rectangle {
                        anchors.bottom: parent.bottom
                        width: parent.width
                        height: 1
                        color: Theme.rule
                    }
                }
            }

            Text {
                visible: root.comments.length === 0
                text: qsTr("还没有人评论。看完可以留一句。")
                color: Theme.ink2
                font.family: Theme.fontText
                font.pixelSize: 12
                renderType: Text.NativeRendering
            }

            // Composer
            Column {
                id: composer
                width: parent.width
                spacing: 8

                Row {
                    width: parent.width
                    visible: root.commentHasMore

                    ActionLink {
                        label: root.loadingMore
                            ? qsTr("加载中…")
                            : qsTr("加载更多（还有 ") + Math.max(0, root.commentTotal - root.comments.length) + qsTr(" 条）")
                        tone: Theme.ink
                        underline: true
                        onActivated: if (!root.loadingMore) root.loadComments(false)
                    }
                }

                Row {
                    width: parent.width
                    spacing: 10
                    visible: root.replyTarget !== null

                    Text {
                        text: root.replyTarget
                            ? qsTr("回复 ") + Http.author(root.replyTarget)
                            : ""
                        color: Theme.ink
                        font.family: Theme.fontText
                        font.pixelSize: 12
                        font.weight: Font.DemiBold
                        renderType: Text.NativeRendering
                    }

                    ActionLink {
                        label: qsTr("取消")
                        tone: Theme.ink2
                        underline: true
                        onActivated: root.replyTarget = null
                    }
                }

                Rectangle {
                    width: parent.width
                    height: 74
                    color: Theme.panel
                    border.color: draftField.activeFocus ? Theme.trace : Theme.rule
                    border.width: draftField.activeFocus ? 2 : 1

                    TextInput {
                        id: draftField
                        anchors.fill: parent
                        anchors.margins: 10
                        text: root.commentDraft
                        color: Theme.ink
                        font.family: Theme.fontText
                        font.pixelSize: 13
                        wrapMode: TextInput.Wrap
                        selectByMouse: true
                        enabled: Api.authenticated && !root.posting
                        selectionColor: Theme.signal
                        selectedTextColor: Theme.onSignal
                        onTextChanged: root.commentDraft = text
                    }

                    Text {
                        anchors.left: parent.left
                        anchors.top: parent.top
                        anchors.margins: 11
                        visible: root.commentDraft === ""
                        text: Api.authenticated ? qsTr("写一句你自己看到的。") : qsTr("登录之后可以评论")
                        color: Theme.ink2
                        font.family: Theme.fontText
                        font.pixelSize: 13
                        renderType: Text.NativeRendering
                    }
                }

                ActionButton {
                    width: parent.width
                    label: Api.authenticated
                        ? (root.posting ? qsTr("发表中…") : qsTr("发表评论"))
                        : qsTr("去登录")
                    variant: "primary"
                    cornerColor: Theme.paper
                    enabled: !root.posting
                    onClicked: {
                        if (Api.authenticated)
                            root.postComment()
                        else if (root.router)
                            root.router.go("login")
                    }
                }
            }
        }
    }

    // Nothing to show: say which of the two reasons it is, and give one way out.
    Item {
        anchors.fill: parent
        anchors.margins: Theme.pagePad
        visible: root.gone

        Rectangle {
            anchors.fill: parent
            color: "transparent"
            border.color: Theme.rule
            border.width: 1
        }

        Hatch {
            anchors.fill: parent
            opacity: 0.2
        }

        Column {
            anchors.centerIn: parent
            spacing: 12

            PairTitle {
                anchors.horizontalCenter: parent.horizontalCenter
                en: root.gonePending ? "IN REVIEW" : "UNAVAILABLE"
                cn: root.gonePending ? qsTr("这条还在审核") : qsTr("这条视频不在了")
            }

            Text {
                anchors.horizontalCenter: parent.horizontalCenter
                width: Math.min(460, root.width - 160)
                horizontalAlignment: Text.AlignHCenter
                wrapMode: Text.WordWrap
                text: root.gonePending
                    ? qsTr("审核通过之后它才会出现在这里和推荐里。你可以在发布页看到它的状态。")
                    : qsTr("作者删除了它，或者链接里的编号不对。回到推荐里还有别的可看。")
                color: Theme.ink
                font.family: Theme.fontText
                font.pixelSize: 13
                lineHeight: 1.6
                renderType: Text.NativeRendering

                Rectangle {
                    anchors.fill: parent
                    anchors.margins: -10
                    color: Theme.paper
                    z: -1
                }
            }

            Row {
                anchors.horizontalCenter: parent.horizontalCenter
                spacing: 10

                ActionButton {
                    label: qsTr("返回推荐")
                    variant: "primary"
                    cornerColor: Theme.paper
                    onClicked: if (root.router) root.router.go("feed")
                }

                ActionButton {
                    label: qsTr("去发布页")
                    variant: "ghost"
                    cornerColor: Theme.paper
                    visible: root.gonePending
                    onClicked: if (root.router) root.router.go("publish")
                }
            }
        }
    }

    function toggleLike() {
        Api.request("like", "PUT", "/v1/likes", {
            target_id: root.videoId,
            target_type: "video",
            active: !root.liked
        })
    }

    function toggleCollect() {
        Api.request("collect", "PUT", "/v1/collect", {
            video_id: root.videoId,
            collected: !root.collected
        })
    }

    Connections {
        target: Api

        function onResult(tag, data) {
            if (tag === "like") {
                var payload = data && data.data ? data.data : data
                root.liked = !!(payload && payload.active)
                root.likeCount = Http.num(payload && payload.like_count !== undefined ? payload.like_count : root.likeCount)
            } else if (tag === "collect") {
                var c = data && data.data ? data.data : data
                root.collected = !!(c && c.collected)
                root.collectCount = Http.num(c && c.collect_count !== undefined ? c.collect_count : root.collectCount)
            }
        }
    }
}
