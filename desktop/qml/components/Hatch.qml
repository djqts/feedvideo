import QtQuick
import FeedVideo.Core 1.0

// 45-degree ink hatching at 22%. Used for empty states, title bands, frame
// shadows and disabled areas — never as decoration on its own.
Canvas {
    id: root

    property color ink: Theme.ink
    property real alpha: 0.22
    property real period: 6.5
    property real lineWidth: 1.5

    renderTarget: Canvas.Image
    antialiasing: true

    function repaint() {
        requestPaint()
    }

    onPaint: {
        var ctx = getContext("2d")
        ctx.reset()
        ctx.clearRect(0, 0, width, height)
        if (width <= 0 || height <= 0)
            return

        ctx.strokeStyle = Qt.rgba(ink.r, ink.g, ink.b, alpha)
        ctx.lineWidth = lineWidth
        ctx.beginPath()
        // Lines rising to the right: from (x, height) to (x + height, 0).
        for (var x = -height; x < width + height; x += period) {
            ctx.moveTo(x, height)
            ctx.lineTo(x + height, 0)
        }
        ctx.stroke()
    }

    onWidthChanged: repaint()
    onHeightChanged: repaint()
    onInkChanged: repaint()
    onAlphaChanged: repaint()
    Component.onCompleted: repaint()
}
