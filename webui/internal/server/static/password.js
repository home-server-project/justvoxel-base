document.querySelectorAll('input[type="password"]').forEach((input) => {
  const shell = document.createElement("span");
  shell.className = "password-field-shell";
  input.parentNode.insertBefore(shell, input);
  shell.appendChild(input);

  const button = document.createElement("button");
  button.type = "button";
  button.className = "password-reveal-button";
  button.setAttribute("aria-label", "Show password");
  button.setAttribute("title", "Show password");
  button.innerHTML = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M2.5 12s3.5-6 9.5-6 9.5 6 9.5 6-3.5 6-9.5 6-9.5-6-9.5-6Z"></path><circle cx="12" cy="12" r="2.8"></circle></svg>';
  button.addEventListener("click", () => {
    const showing = input.type === "text";
    input.type = showing ? "password" : "text";
    button.classList.toggle("is-showing", !showing);
    button.setAttribute("aria-label", showing ? "Show password" : "Hide password");
    button.setAttribute("title", showing ? "Show password" : "Hide password");
  });
  shell.appendChild(button);

  if (input.name === "new_password") {
    const strength = document.createElement("span");
    strength.className = "password-strength is-unacceptable";
    strength.setAttribute("role", "status");
    strength.textContent = "Strength: Not acceptable";
    shell.after(strength);
    input.addEventListener("input", () => {
      const value = input.value;
      const classes = [/[a-z]/, /[A-Z]/, /[0-9]/, /[^a-zA-Z0-9]/].filter((pattern) => pattern.test(value)).length;
      const minimum = Math.max(8, input.minLength > 0 ? input.minLength : 8);
      const level = value.length < minimum || classes < 2 ? "unacceptable" : value.length >= 12 && classes >= 3 ? "strong" : "acceptable";
      strength.className = "password-strength is-" + level;
      strength.textContent = "Strength: " + (level === "unacceptable" ? "Not acceptable" : level === "strong" ? "Strong" : "Acceptable");
    });
  }
});
