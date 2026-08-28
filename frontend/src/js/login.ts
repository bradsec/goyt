const form = document.querySelector<HTMLFormElement>('#login-form');
const errorElement = document.querySelector<HTMLElement>('#error');
const passwordInput = document.querySelector<HTMLInputElement>('#password');

if (!form || !errorElement || !passwordInput) {
  throw new Error('Login form is incomplete');
}

form.addEventListener('submit', async (event) => {
  event.preventDefault();
  errorElement.textContent = '';

  try {
    const response = await fetch('/api/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ password: passwordInput.value }),
    });
    if (response.ok) {
      window.location.assign('/');
      return;
    }
    if (response.status === 429) {
      errorElement.textContent = 'Too many attempts. Please wait and try again.';
    } else if (response.status >= 500) {
      errorElement.textContent = 'Server error. Please try again.';
    } else {
      errorElement.textContent = 'Incorrect password.';
    }
  } catch {
    errorElement.textContent = 'Network error. Please try again.';
  }
});
