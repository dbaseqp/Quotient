// auto set theme based on system settings
const getStoredTheme = () => localStorage.getItem('theme')

const getPreferredTheme = () => {
    const storedTheme = getStoredTheme()
    if (storedTheme) {
        return storedTheme
    }
    return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

const resolveThemeMode = (theme) => {
    if (theme === 'dark' || theme === 'light') {
        return theme
    }
    return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

window.getResolvedTheme = () => {
    return document.documentElement.getAttribute('data-bs-theme') === 'dark' ? 'dark' : 'light'
}

window.renderSafeMarkdown = (markdownText) => {
    const container = document.createElement('div')
    const raw = String(markdownText ?? '')
    if (typeof marked === 'undefined' || typeof marked.parse !== 'function') {
        const p = document.createElement('p')
        p.className = 'mb-0'
        p.style.whiteSpace = 'pre-wrap'
        p.textContent = raw
        container.appendChild(p)
        return container
    }

    const parsedHTML = marked.parse(raw, { breaks: true, gfm: true })
    const doc = new DOMParser().parseFromString(parsedHTML, 'text/html')
    const ALLOWED_TAGS = new Set([
        'P', 'BR', 'STRONG', 'B', 'EM', 'I', 'U', 'S', 'DEL',
        'CODE', 'PRE', 'BLOCKQUOTE', 'UL', 'OL', 'LI',
        'H1', 'H2', 'H3', 'H4', 'H5', 'H6', 'HR',
        'TABLE', 'THEAD', 'TBODY', 'TR', 'TH', 'TD', 'A'
    ])

    const sanitizeNode = (node) => {
        if (node.nodeType === Node.TEXT_NODE) {
            return document.createTextNode(node.textContent)
        }
        if (node.nodeType !== Node.ELEMENT_NODE) {
            return null
        }
        const tag = node.tagName.toUpperCase()
        if (!ALLOWED_TAGS.has(tag)) {
            const fragment = document.createDocumentFragment()
            node.childNodes.forEach(child => {
                const cleanChild = sanitizeNode(child)
                if (cleanChild) fragment.appendChild(cleanChild)
            })
            return fragment
        }

        const el = document.createElement(tag.toLowerCase())
        if (tag === 'A' && node.hasAttribute('href')) {
            const href = (node.getAttribute('href') || '').trim()
            if (/^(https?:|mailto:|\/|#)/i.test(href) && !/^javascript:/i.test(href)) {
                el.setAttribute('href', href)
                el.setAttribute('target', '_blank')
                el.setAttribute('rel', 'noopener noreferrer')
            }
        }
        if (tag === 'TABLE') {
            el.className = 'table table-sm table-bordered my-2'
        }
        if (tag === 'PRE') {
            el.className = 'bg-body-secondary p-2 rounded overflow-x-auto'
        }
        node.childNodes.forEach(child => {
            const cleanChild = sanitizeNode(child)
            if (cleanChild) el.appendChild(cleanChild)
        })
        return el
    }

    doc.body.childNodes.forEach(child => {
        const cleanChild = sanitizeNode(child)
        if (cleanChild) container.appendChild(cleanChild)
    })
    return container
}

;(() => {
    // load theme
    'use strict'

    const setStoredTheme = theme => localStorage.setItem('theme', theme)

    const setTheme = theme => {
        const resolved = resolveThemeMode(theme)
        document.documentElement.setAttribute('data-bs-theme', resolved)
        window.dispatchEvent(new CustomEvent('quotient:themechange', {
            detail: { theme: resolved, preference: theme }
        }))
    }

    setTheme(getPreferredTheme())

    const showActiveTheme = (theme, focus = false) => {
        const themeSwitcher = document.querySelector('#bd-theme')

        if (!themeSwitcher) {
            return
        }

        const themeSwitcherText = document.querySelector('#bd-theme-text')
        const activeThemeIcon = document.querySelector('i.theme-icon-active')
        const btnToActive = document.querySelector(`[data-bs-theme-value="${theme}"]`) ||
            document.querySelector('[data-bs-theme-value="auto"]')
        if (!btnToActive || !activeThemeIcon) {
            return
        }
        const svgOfActiveBtn = btnToActive.querySelector('i.bi').getAttribute('class')

        document.querySelectorAll('[data-bs-theme-value]').forEach(element => {
            element.classList.remove('active')
            element.setAttribute('aria-pressed', 'false')
        })

        const activeClasses = activeThemeIcon.getAttribute('class').split(' ')
        activeClasses[1] = svgOfActiveBtn.split(' ')[1]
        const newClasses = activeClasses.join(' ')

        btnToActive.classList.add('active')
        btnToActive.setAttribute('aria-pressed', 'true')
        activeThemeIcon.setAttribute('class', newClasses)
        const labelPrefix = (themeSwitcherText && themeSwitcherText.textContent.trim()) ? themeSwitcherText.textContent.trim() : 'Toggle theme'
        const themeSwitcherLabel = `${labelPrefix} (${btnToActive.dataset.bsThemeValue})`
        themeSwitcher.setAttribute('aria-label', themeSwitcherLabel)

        if (focus) {
            themeSwitcher.focus()
        }
    }

    window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {
        const storedTheme = getStoredTheme()
        if (storedTheme !== 'light' && storedTheme !== 'dark') {
            setTheme(getPreferredTheme())
        }
    })

    window.addEventListener('DOMContentLoaded', () => {
        showActiveTheme(getPreferredTheme())

        document.querySelectorAll('[data-bs-theme-value]')
            .forEach(toggle => {
                toggle.addEventListener('click', () => {
                    const theme = toggle.getAttribute('data-bs-theme-value')
                    setStoredTheme(theme)
                    setTheme(theme)
                    showActiveTheme(theme, true)
                })
            })
    })
})()
