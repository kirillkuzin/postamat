import './styles.css'

const app = document.querySelector<HTMLDivElement>('#app')

if (app) {
  app.innerHTML = `
    <section class="card" aria-labelledby="recipient-title">
      <p class="eyebrow">postamat</p>
      <h1 id="recipient-title">Secure transfer recipient</h1>
      <p>
        The browser recipient flow will connect to postamat signaling in the next milestone.
      </p>
    </section>
  `
}
