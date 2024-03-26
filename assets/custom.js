// auto set theme based on system settings
const getStoredTheme = () => localStorage.getItem('theme')

const getPreferredTheme = () => {
    const storedTheme = getStoredTheme()
    if (storedTheme) {
        return storedTheme
    }
    return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

(() => {
    'use strict'

    const setStoredTheme = theme => localStorage.setItem('theme', theme)

    const setTheme = theme => {
        if (theme === 'auto' && window.matchMedia('(prefers-color-scheme: dark)').matches) {
            document.documentElement.setAttribute('data-bs-theme', 'dark')
        } else {
            document.documentElement.setAttribute('data-bs-theme', theme)
        }
    }

    setTheme(getPreferredTheme())

    const showActiveTheme = (theme, focus = false) => {
        const themeSwitcher = document.querySelector('#bd-theme')

        if (!themeSwitcher) {
            return
        }

        const themeSwitcherText = document.querySelector('#bd-theme-text')
        const activeThemeIcon = document.querySelector('i.theme-icon-active')
        const btnToActive = document.querySelector(`[data-bs-theme-value="${theme}"]`)
        const svgOfActiveBtn = btnToActive.querySelector('i.bi').getAttribute('class')

        document.querySelectorAll('[data-bs-theme-value]').forEach(element => {
            element.classList.remove('active')
            element.setAttribute('aria-pressed', 'false')
        })

        var activeClasses = activeThemeIcon.getAttribute('class').split(' ')
        activeClasses[1] = svgOfActiveBtn.split(' ')[1]
        const newClasses = activeClasses.join(' ')

        btnToActive.classList.add('active')
        btnToActive.setAttribute('aria-pressed', 'true')
        activeThemeIcon.setAttribute('class', newClasses)
        const themeSwitcherLabel = `${themeSwitcherText.textContent} (${btnToActive.dataset.bsThemeValue})`
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

function createToast(message, color, delay) {
    const toastContainer = document.getElementById('toast-container');

    // Create a new toast element
    const toast = document.createElement('div');
    toast.className = 'toast';
    toast.innerHTML = `
        <div class="toast-header ${color}">
        <strong class="me-auto">Notification</strong>
        <button type="button" class="btn-close" data-bs-dismiss="toast"></button>
        </div>
        <div class="toast-body">
        ${message}
        </div>
    `;

    // Add the toast to the container
    toast.setAttribute("data-bs-autohide", false)
    toastContainer.appendChild(toast);

    // Initialize the Bootstrap toast
    const bootstrapToast = new bootstrap.Toast(toast);

    // Show the toast
    bootstrapToast.show();

    // Automatically hide the toast after the specified delay
    if (delay) {
        setTimeout(() => {
            bootstrapToast.hide();
        }, delay);
    }
}

function postAjax(e, formid, data, url, success_function) {
    e.preventDefault()
    showLoading()
    fetch(url, {
        method: "post",
        body: data,
    })
        .then((response) => {
            hideLoading()
            if (!response.ok) {
                Promise.reject(response);
            }
            return response.json();
        })
        .then((data) => {
            if (data.error) {
                createToast(data.error, "bg-danger")
            } else {
                success_function(data)
            }
        })
        .catch(error => {
            createToast(error, "bg-danger")
        })
}

function formatTime() {
    const times = document.querySelectorAll(".time")
    times.forEach((time) => {
        let date = new Date(time.getAttribute("data-time"))
        let now = new Date()
        if (date < new Date().setFullYear(2001)) {
            // launctime always
            time.textContent = "*"
        } else if (date > new Date().setFullYear(now.getFullYear() + 2)) {
            // stoptime never ends
            time.textContent = "*"
        } else {
            time.textContent = date.toLocaleString()
        }
    })
}

function showLoading() {
    const modal = bootstrap.Modal.getOrCreateInstance(document.getElementById('loadingModal'))
    modal.show()
}

function hideLoading() {
    document.getElementById("loadingModal").addEventListener('shown.bs.modal', (event) => {
        const modal = bootstrap.Modal.getOrCreateInstance(document.getElementById('loadingModal'))
        modal.hide()
    })
}