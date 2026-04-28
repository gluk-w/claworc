document.addEventListener('DOMContentLoaded', function () {
    // Smooth scroll for anchor links
    document.querySelectorAll('a[href^="#"]').forEach(function (link) {
        link.addEventListener('click', function (e) {
            var targetId = this.getAttribute('href');
            if (targetId === '#') return;
            var target = document.querySelector(targetId);
            if (target) {
                e.preventDefault();
                target.scrollIntoView({ behavior: 'smooth', block: 'start' });
            }
        });
    });

    // Screenshot sliders
    document.querySelectorAll('[data-slider]').forEach(function (slider) {
        var images = slider.querySelectorAll('.slider-track img');
        var dots = slider.querySelectorAll('.slider-dot');
        dots.forEach(function (dot) {
            dot.addEventListener('click', function () {
                var index = parseInt(this.getAttribute('data-slide'));
                images.forEach(function (img, i) {
                    img.classList.toggle('hidden', i !== index);
                });
                dots.forEach(function (d, i) {
                    d.classList.toggle('active', i === index);
                });
            });
        });
    });

    // Copy-to-clipboard for install command
    var copyBtn = document.getElementById('copy-install');
    if (copyBtn) {
        copyBtn.addEventListener('click', function () {
            var code = document.getElementById('install-command');
            if (!code) return;
            var text = code.textContent.trim();
            navigator.clipboard.writeText(text).then(function () {
                var original = copyBtn.innerHTML;
                copyBtn.innerHTML = '<svg class="w-5 h-5 text-green-400" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7"/></svg>';
                setTimeout(function () {
                    copyBtn.innerHTML = original;
                }, 2000);
            });
        });
    }
});
