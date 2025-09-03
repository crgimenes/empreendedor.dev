# empreendedor.dev

**Useful tools for developers - built as a real, minimalist system you can learn from.**

> _empreendedor.dev_ has two core goals:
>
> 1) deliver genuinely useful tools for developers;
> 2) serve as a concrete, end‑to‑end example of building systems with simplicity and minimalism.

---

## Links

- **Repository:** https://github.com/crgimenes/empreendedor.dev
- **Issues (features & bugs):** https://github.com/crgimenes/empreendedor.dev/issues

> Please use **GitHub Issues** to request new features or report bugs. Clear steps to reproduce, expected vs. actual behavior, and environment details help a lot.

---

## Why this project exists

This repository was born from the Brazilian **Go Study Group (Grupo de Estudos de Go)**, where we have been meeting for years to study Go and share practical techniques to build software without unnecessary complexity. Explaining simplicity in the abstract is hard; showing it in a real system is easier.

We decided to build a real service-initially similar to a job‑board idea. We even registered `empregos.dev.br`, but since many similar domains already existed, we broadened the scope and registered **`empreendedor.dev`**. It will implement job‑board functionality **and** other useful tools, without being limited to that. The goal is to demonstrate each decision clearly and transparently, using real, working code.

---

## Project philosophy

- **Minimalism first.** Prefer the Go standard library and small, explicit code.
- **No premature scalability.** Scale when reality-not fear-demands it.
- **Be cautious with dependencies.** Add external packages only when the value clearly outweighs the cost.
- **Simple configuration.** A few well‑named environment variables beat elaborate configuration systems.
- **Transparent decisions.** We document the “why”, not just the “what”.
- **Security & reliability.** Sensible defaults; fail fast; handle errors explicitly; keep attack surface small.

---

## Status

**Pre‑alpha.** Active development. Interfaces and layout may change.

---

## Contributing

We welcome ideas, questions, and patches.

- **Feature requests & bug reports:** use **GitHub Issues** → https://github.com/crgimenes/empreendedor.dev/issues
- **Pull requests:** keep them small and focused; explain the intent and trade‑offs; include tests where it makes sense.
- **Dependencies:** propose external packages only with strong justification; prefer the standard library.
- **Style:** follow [Effective Go], [Code Review Comments], and keep configuration and IO boundaries explicit.

If you’re unsure whether something fits the philosophy, open an Issue first-discussion is welcome.

---

## Security

Do not open public Issues for sensitive vulnerabilities. Please use GitHub Security Advisories (preferred) or contact a maintainer privately. We aim for:

- explicit timeouts, input validation, and clear error handling;
- least‑privilege defaults and a small dependency surface.

---

## License

[MIT](LICENSE)

---

## Acknowledgments

Thanks to the Brazilian **Go Study Group** community for years of discussion, code reviews, and real‑world lessons that shaped this project.

---

## References

- [Effective Go](https://go.dev/doc/effective_go)
- [Go Modules – Managing Dependencies](https://go.dev/doc/modules/managing-dependencies)
- [Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments)
- [OWASP Top Ten](https://owasp.org/www-project-top-ten/)
- [GitHub Issues Guide](https://docs.github.com/issues)

---

> _“Simple is not easy. We choose boring, obvious solutions first-and prove they’re enough before reaching for more.”_
