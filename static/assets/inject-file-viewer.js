(() => {
    "use strict"

    const MAX_DOCX_BYTES = 10 * 1024 * 1024
    const MAX_DOCX_EXPANDED_BYTES = 64 * 1024 * 1024
    const MAX_DOCX_ENTRIES = 2000

    const viewer = document.getElementById("inject-file-viewer")
    const title = document.getElementById("inject-file-viewer-title")
    const download = document.getElementById("inject-file-download")
    const body = document.getElementById("inject-file-viewer-body")
    const loading = document.getElementById("inject-file-loading")
    const error = document.getElementById("inject-file-error")
    const pdfFrame = document.getElementById("inject-pdf-frame")
    const docxContainer = document.getElementById("inject-docx-container")

    let controller
    let requestVersion = 0
    let activeKind = ""

    function injectFileURL(injectID, fileName) {
        return `/injects/${encodeURIComponent(injectID)}/${encodeURIComponent(fileName)}`
    }

    function setLoading(isLoading) {
        loading.classList.toggle("d-none", !isLoading)
        body.setAttribute("aria-busy", String(isLoading))
    }

    function showError(message) {
        setLoading(false)
        error.textContent = message
        error.classList.remove("d-none")
    }

    function clearViewer() {
        requestVersion++
        activeKind = ""
        controller?.abort()
        controller = undefined
        pdfFrame.removeAttribute("src")
        pdfFrame.classList.add("d-none")
        docxContainer.replaceChildren()
        docxContainer.classList.add("d-none")
        error.classList.add("d-none")
        error.textContent = ""
        setLoading(false)
    }

    async function readResponseWithLimit(response, limit) {
        const declaredLength = Number(response.headers.get("Content-Length"))
        if (Number.isFinite(declaredLength) && declaredLength > limit) {
            throw new Error("This document is too large to preview. Download it instead.")
        }

        if (!response.body) {
            const buffer = await response.arrayBuffer()
            if (buffer.byteLength > limit) {
                throw new Error("This document is too large to preview. Download it instead.")
            }
            return buffer
        }

        const reader = response.body.getReader()
        const chunks = []
        let totalLength = 0

        while (true) {
            const { done, value } = await reader.read()
            if (done) {
                break
            }
            totalLength += value.byteLength
            if (totalLength > limit) {
                await reader.cancel()
                throw new Error("This document is too large to preview. Download it instead.")
            }
            chunks.push(value)
        }

        const bytes = new Uint8Array(totalLength)
        let offset = 0
        for (const chunk of chunks) {
            bytes.set(chunk, offset)
            offset += chunk.byteLength
        }
        return bytes.buffer
    }

    async function validateDocxArchive(buffer) {
        if (!window.JSZip) {
            throw new Error("The DOCX preview library could not be loaded.")
        }

        let archive
        try {
            archive = await window.JSZip.loadAsync(buffer)
        } catch {
            throw new Error("This file is not a valid DOCX document.")
        }

        const entries = Object.values(archive.files)
        if (entries.length > MAX_DOCX_ENTRIES) {
            throw new Error("This document contains too many files to preview safely.")
        }
        if (!archive.file("[Content_Types].xml") || !archive.file("word/document.xml")) {
            throw new Error("This file is not a valid DOCX document.")
        }

        let expandedBytes = 0
        for (const entry of entries) {
            if (entry.dir) {
                continue
            }
            const entrySize = entry._data?.uncompressedSize
            if (!Number.isFinite(entrySize) || entrySize < 0) {
                throw new Error("This document could not be inspected safely.")
            }
            expandedBytes += entrySize
            if (expandedBytes > MAX_DOCX_EXPANDED_BYTES) {
                throw new Error("This document expands beyond the preview safety limit. Download it instead.")
            }
        }
    }

    function secureRenderedLinks() {
        docxContainer.querySelectorAll("a[href]").forEach(link => {
            const href = link.getAttribute("href")?.trim()
            if (!href || href.startsWith("#")) {
                return
            }

            let url
            try {
                url = new URL(href, window.location.href)
            } catch {
                link.removeAttribute("href")
                return
            }

            if (url.protocol !== "http:" && url.protocol !== "https:" && url.protocol !== "mailto:") {
                link.removeAttribute("href")
                return
            }
            link.target = "_blank"
            link.rel = "noopener noreferrer"
        })
    }

    async function renderDocx(fileURL, version) {
        if (!window.docx?.renderAsync) {
            throw new Error("The DOCX preview library could not be loaded.")
        }

        controller = new AbortController()
        const response = await fetch(`${fileURL}?inline=1`, {
            credentials: "same-origin",
            signal: controller.signal,
        })
        if (!response.ok) {
            throw new Error(`Unable to load this document (HTTP ${response.status}).`)
        }

        const buffer = await readResponseWithLimit(response, MAX_DOCX_BYTES)
        await validateDocxArchive(buffer)
        if (version !== requestVersion) {
            return
        }

        await window.docx.renderAsync(buffer, docxContainer, docxContainer, {
            breakPages: true,
            debug: false,
            experimental: false,
            ignoreFonts: true,
            ignoreLastRenderedPageBreak: false,
            renderAltChunks: false,
            renderChanges: false,
            renderComments: false,
            renderEndnotes: true,
            renderFooters: true,
            renderFootnotes: true,
            renderHeaders: true,
            useBase64URL: true,
        })
        if (version !== requestVersion) {
            return
        }

        secureRenderedLinks()
        setLoading(false)
    }

    async function open(injectID, fileName) {
        clearViewer()
        const version = requestVersion
        const fileURL = injectFileURL(injectID, fileName)
        const extension = fileName.toLowerCase().split(".").pop()

        title.textContent = fileName
        download.href = fileURL
        download.setAttribute("download", fileName)
        bootstrap.Offcanvas.getOrCreateInstance(viewer).show()
        setLoading(true)

        if (extension === "pdf") {
            activeKind = "pdf"
            pdfFrame.classList.remove("d-none")
            pdfFrame.src = `${fileURL}?inline=1`
            return
        }

        if (extension !== "docx") {
            showError("Inline preview is not available for this file type. Download it instead.")
            return
        }

        activeKind = "docx"
        docxContainer.classList.remove("d-none")
        try {
            await renderDocx(fileURL, version)
        } catch (renderError) {
            if (renderError.name !== "AbortError" && version === requestVersion) {
                docxContainer.replaceChildren()
                docxContainer.classList.add("d-none")
                showError(renderError.message || "This document could not be previewed. Download it instead.")
            }
        }
    }

    pdfFrame.addEventListener("load", () => {
        if (activeKind === "pdf") {
            setLoading(false)
        }
    })
    viewer.addEventListener("hidden.bs.offcanvas", clearViewer)

    window.InjectFileViewer = { open, validateDocxArchive }
})()
