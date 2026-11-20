package bom_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/sclevine/agouti"
	"github.com/sclevine/agouti/bom"
)

func newTestServer(body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		response.Write([]byte(body))
	}))
}

// The page below models a production-planning screen. Recipes expand lazily:
// clicking an expander shows a loading placeholder, then the lines. The same
// shared sub-assembly S is rendered under both finished products, and each
// render repeats the same usage-line IDs, which the collector must count only
// once.
const bomPage = `<!DOCTYPE html>
<html>
<head>
<style>
  .bom-node { border: 1px solid #ccc; margin: 4px; padding: 4px; }
  .bom-loading { display: none; color: #999; }
  .bom-loading.active { display: block; }
</style>
<script>
  var RECIPES = {
    "v1|P": [
      {id: "P-1", version: "v1", material: "S", qty: 2},
      {id: "P-2", version: "v1", material: "S", qty: 1},
      {id: "P-3", version: "v1", material: "X", qty: 3}
    ],
    "v1|S": [
      {id: "S-1", version: "v1", material: "Y", qty: 4},
      {id: "S-2", version: "v1", material: "Y", qty: 1}
    ],
    "v1|Q": [
      {id: "Q-1", version: "v1", material: "S", qty: 5}
    ]
  };

  var LEAVES = {"X": true, "Y": true};

  function makeNode(version, material, leaf) {
    var node = document.createElement("div");
    node.className = "bom-node";
    node.setAttribute("data-version", version);
    node.setAttribute("data-material-id", material);
    node.setAttribute("data-leaf", leaf ? "true" : "false");
    node.setAttribute("data-expanded", leaf ? "true" : "false");
    var header = document.createElement("span");
    header.textContent = version + "/" + material;
    node.appendChild(header);
    if (!leaf) {
      var button = document.createElement("button");
      button.className = "bom-expand";
      button.textContent = "Expand " + version + "/" + material;
      node.appendChild(button);
      var loading = document.createElement("div");
      loading.className = "bom-loading";
      loading.textContent = "Loading...";
      node.appendChild(loading);
    }
    return node;
  }

  function renderMaterial(version, material, leaf) {
    var node = makeNode(version, material, leaf);
    document.getElementById("tree").appendChild(node);
    return node;
  }
</script>
</head>
<body>
  <div class="bom-order" data-order-id="O1" data-version="v1" data-material-id="P" data-quantity="2"></div>
  <div class="bom-order" data-order-id="O2" data-version="v1" data-material-id="Q" data-quantity="1"></div>
  <div id="tree"></div>
  <table id="stock">
    <tr class="bom-stock" data-version="v1" data-material-id="X" data-name="Steel plate" data-supplier="Acme" data-unit-cost-cents="250" data-on-hand="1"></tr>
    <tr class="bom-stock" data-version="v1" data-material-id="Y" data-name="Copper wire" data-supplier="Bolt Co" data-unit-cost-cents="40" data-on-hand="10"></tr>
  </table>
  <script>
    // Clicking an expander simulates an async load: the placeholder appears,
    // then the lines and child nodes are revealed. Every usage line renders a
    // child node, so a shared sub-assembly appears once per referencing line.
    document.addEventListener("click", function(event) {
      var button = event.target;
      if (!button.classList || !button.classList.contains("bom-expand")) {
        return;
      }
      var node = button.parentNode;
      var version = node.getAttribute("data-version");
      var material = node.getAttribute("data-material-id");
      var loading = node.querySelector(".bom-loading");
      loading.classList.add("active");

      setTimeout(function() {
        var lines = document.createElement("div");
        lines.className = "bom-lines";
        var recipe = RECIPES[version + "|" + material] || [];
        recipe.forEach(function(line) {
          var row = document.createElement("div");
          row.className = "bom-line";
          row.setAttribute("data-line-id", line.id);
          row.setAttribute("data-child-version", line.version);
          row.setAttribute("data-child-material-id", line.material);
          row.setAttribute("data-quantity", String(line.qty));
          row.textContent = line.id + " -> " + line.material + " x" + line.qty;
          lines.appendChild(row);
        });
        node.appendChild(lines);
        loading.classList.remove("active");
        node.setAttribute("data-expanded", "true");

        recipe.forEach(function(line) {
          renderMaterial(line.version, line.material, !!LEAVES[line.material]);
        });
      }, 50);
    });

    renderMaterial("v1", "P", false);
    renderMaterial("v1", "Q", false);
  </script>
</body>
</html>`

func phantomJSAvailable() bool {
	_, err := exec.LookPath("phantomjs")
	return err == nil
}

var _ = Describe("PageSource", func() {
	var (
		driver *agouti.WebDriver
		page   *agouti.Page
		server *httptest.Server
	)

	BeforeEach(func() {
		if !phantomJSAvailable() {
			Skip("phantomjs is not available")
		}
		server = newTestServer(bomPage)

		driver = agouti.PhantomJS()
		Expect(driver.Start()).To(Succeed())

		var err error
		page, err = driver.NewPage()
		Expect(err).NotTo(HaveOccurred())
		Expect(page.Navigate(server.URL)).To(Succeed())
	})

	AfterEach(func() {
		if server == nil {
			return
		}
		Expect(page.Destroy()).To(Succeed())
		Expect(driver.Stop()).To(Succeed())
		server.Close()
	})

	It("expands the page, counts shared recipes once and deducts stock once", func() {
		source := bom.NewPageSource(page)
		source.Timeout = 5 * time.Second
		Expect(source.Collect([]string{"O1", "O2"})).To(Succeed())

		var details, report bytes.Buffer
		result, err := bom.Daily(source, []string{"O1", "O2"}, &details, &report)
		Expect(err).NotTo(HaveOccurred())

		// O1: P x2. O2: Q x1.
		// Y per S = 4 + 1 = 5; S per P = 2 + 1 = 3; X per P = 3.
		// Y demand = 2*3*5 + 1*5*5 = 30 + 25 = 55; X demand = 2*3 = 6.
		// Stock: X 1 -> net 5; Y 10 -> net 45.
		requirements := map[string]int64{}
		for _, requirement := range result.Requirements {
			requirements[requirement.Key.MaterialID] = requirement.NetPurchase
		}
		Expect(requirements).To(Equal(map[string]int64{"X": 5, "Y": 45}))

		// Gross cost: X 6*250 + Y 55*40 = 3700.
		Expect(result.GrossCostCents).To(Equal(int64(3700)))
		// Net cost: X 5*250 + Y 45*40 = 3050.
		Expect(result.NetPurchaseCostCents).To(Equal(int64(3050)))

		Expect(result.Suppliers).To(HaveLen(2))

		// Source versions and usage-line IDs survive into the details.
		Expect(details.String()).To(ContainSubstring("P-1"))
		Expect(details.String()).To(ContainSubstring("v1,Y"))
		Expect(report.String()).To(ContainSubstring("Purchasing daily report"))
	})

	It("does not treat an empty expansion region as a leaf", func() {
		emptyPage := strings.Replace(bomPage, `"v1|S": [
      {id: "S-1", version: "v1", material: "Y", qty: 4},
      {id: "S-2", version: "v1", material: "Y", qty: 1}
    ],`, `"v1|S": [],`, 1)
		Expect(emptyPage).NotTo(Equal(bomPage))

		emptyServer := newTestServer(emptyPage)
		defer emptyServer.Close()

		emptyPageObject, err := driver.NewPage()
		Expect(err).NotTo(HaveOccurred())
		defer emptyPageObject.Destroy()
		Expect(emptyPageObject.Navigate(emptyServer.URL)).To(Succeed())

		source := bom.NewPageSource(emptyPageObject)
		source.Timeout = 5 * time.Second
		err = source.Collect([]string{"O1"})
		Expect(err).To(HaveOccurred())
		Expect(bom.Kind(err)).To(Equal(bom.ErrEmptyExpansion))
	})
})
