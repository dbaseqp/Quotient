(() => {
    "use strict"

    const UPDATE_INTERVAL = 1000
    const WARNING_THRESHOLD = 30 * 60 * 1000
    let timerID

    function formatDuration(milliseconds) {
        const totalSeconds = Math.max(0, Math.ceil(milliseconds / 1000))
        const days = Math.floor(totalSeconds / 86400)
        const hours = Math.floor((totalSeconds % 86400) / 3600)
        const minutes = Math.floor((totalSeconds % 3600) / 60)
        const seconds = totalSeconds % 60

        if (days > 0) {
            return hours > 0 ? `${days}d ${hours}h` : `${days}d`
        }
        if (hours > 0) {
            return `${hours}h ${String(minutes).padStart(2, "0")}m`
        }
        if (minutes > 0) {
            return `${minutes}m ${String(seconds).padStart(2, "0")}s`
        }
        return `${seconds}s`
    }

    function stateFor(element, now) {
        const openTime = new Date(element.dataset.openTime)
        const dueTime = new Date(element.dataset.dueTime)
        const closeTime = new Date(element.dataset.closeTime)

        if (now < openTime) {
            return {
                className: "text-bg-secondary",
                label: `Opens in ${formatDuration(openTime - now)}`,
            }
        }
        if (now < dueTime) {
            return {
                className: dueTime - now <= WARNING_THRESHOLD ? "text-bg-warning" : "text-bg-primary",
                label: `Due in ${formatDuration(dueTime - now)}`,
            }
        }
        if (now < closeTime) {
            return {
                className: "text-bg-danger",
                label: `Late · closes in ${formatDuration(closeTime - now)}`,
            }
        }
        return { className: "text-bg-secondary", label: "Closed" }
    }

    function update(element, now = new Date()) {
        const state = stateFor(element, now)
        element.classList.remove("text-bg-primary", "text-bg-warning", "text-bg-danger", "text-bg-secondary")
        element.classList.add(state.className)
        element.textContent = state.label
        element.setAttribute("aria-label", state.label)
    }

    function updateAll() {
        const now = new Date()
        document.querySelectorAll(".inject-countdown-badge").forEach(element => update(element, now))
    }

    function bind(element, inject) {
        element.className = "badge rounded-pill inject-countdown-badge"
        element.dataset.openTime = inject.OpenTime.toISOString()
        element.dataset.dueTime = inject.DueTime.toISOString()
        element.dataset.closeTime = inject.CloseTime.toISOString()
        element.setAttribute("aria-live", "off")
        update(element)
    }

    function start() {
        if (timerID !== undefined) {
            return
        }
        timerID = window.setInterval(updateAll, UPDATE_INTERVAL)
        window.addEventListener("pagehide", () => window.clearInterval(timerID), { once: true })
    }

    window.InjectCountdown = { bind, formatDuration, stateFor, updateAll }
    start()
})()
